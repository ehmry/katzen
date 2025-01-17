package main

import (
	"github.com/hako/durafmt"
	"github.com/katzenpost/katzenpost/catshadow"
	"golang.org/x/exp/shiny/materialdesign/icons"
	"image"
	"runtime"
	"strings"
	"time"

	"gioui.org/gesture"
	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var (
	messageList      = &layout.List{Axis: layout.Vertical, ScrollToEnd: true}
	messageField     = &widget.Editor{SingleLine: true}
	backIcon, _      = widget.NewIcon(icons.NavigationChevronLeft)
	sendIcon, _      = widget.NewIcon(icons.NavigationChevronRight)
	queuedIcon, _    = widget.NewIcon(icons.NotificationSync)
	sentIcon, _      = widget.NewIcon(icons.ActionDone)
	deliveredIcon, _ = widget.NewIcon(icons.ActionDoneAll)
	pandaIcon, _     = widget.NewIcon(icons.ActionPets)
)

type conversationPage struct {
	a              *App
	nickname       string
	avatar         *widget.Image
	edit           *gesture.Click
	compose        *widget.Editor
	send           *widget.Clickable
	back           *widget.Clickable
	cancel         *gesture.Click
	msgcopy        *widget.Clickable
	msgpaste       *LongPress
	msgdetails     *widget.Clickable
	messageClicked *catshadow.Message
	messageClicks  map[*catshadow.Message]*gesture.Click
}

func (c *conversationPage) Start(stop <-chan struct{}) {
}

type MessageSent struct {
	nickname string
	msgId    catshadow.MessageID
}

type EditContact struct {
	nickname string
}

func (c *conversationPage) Event(gtx layout.Context) interface{} {
	// receive keystroke to editor panel
	for _, ev := range c.compose.Events() {
		switch ev.(type) {
		case widget.SubmitEvent:
			c.send.Click()
		}
	}
	for _, ev := range c.msgpaste.Events(gtx.Queue) {
		switch ev.Type {
		case LongPressed:
			clipboard.ReadOp{Tag: c}.Add(gtx.Ops)
			return RedrawEvent{}
		default:
			// return focus to the editor
			c.compose.Focus()
		}
	}
	key.InputOp{Tag: c, Keys: shortcuts}.Add(gtx.Ops)
	for _, e := range gtx.Events(c) {
		switch e := e.(type) {
		case key.Event:
			if e.Name == key.NameEscape && e.State == key.Release {
				return BackEvent{}
			}
			if e.Name == key.NameF5 && e.State == key.Release {
				return EditContact{nickname: c.nickname}
			}
			if e.Name == key.NameUpArrow && e.State == key.Release {
				messageList.ScrollToEnd = false
				if messageList.Position.First > 0 {
					messageList.Position.First = messageList.Position.First - 1
				}

			}
			if e.Name == key.NameDownArrow && e.State == key.Release {
				messageList.ScrollToEnd = true
				messageList.Position.First = messageList.Position.First + 1
			}
			if e.Name == key.NamePageUp && e.State == key.Release {
				messageList.ScrollToEnd = false
				if messageList.Position.First-messageList.Position.Count > 0 {
					messageList.Position.First = messageList.Position.First - messageList.Position.Count
				}

			}
			if e.Name == key.NamePageDown && e.State == key.Release {
				messageList.ScrollToEnd = true
				messageList.Position.First = messageList.Position.First + messageList.Position.Count
			}
			return RedrawEvent{}

		case clipboard.Event:
			if c.compose.SelectionLen() > 0 {
				c.compose.Delete(1) // deletes the selection as a single rune
			}
			start, _ := c.compose.Selection()
			txt := c.compose.Text()
			c.compose.SetText(txt[:start] + e.Text + txt[start:])
			c.compose.Focus()
		}
	}

	if c.send.Clicked() {
		msg := []byte(c.compose.Text())
		c.compose.SetText("")
		if len(msg) == 0 {
			return nil
		}
		// truncate messages
		// TODO: this should split messages and return the set of message IDs sent
		if len(msg)+4 > catshadow.DoubleRatchetPayloadLength {
			msg = msg[:catshadow.DoubleRatchetPayloadLength-4]
		}
		msgId := c.a.c.SendMessage(c.nickname, msg)
		return MessageSent{nickname: c.nickname, msgId: msgId}
	}
	for _, e := range c.edit.Events(gtx.Queue) {
		if e.Type == gesture.TypeClick {
			return EditContact{nickname: c.nickname}
		}
	}
	if c.back.Clicked() {
		return BackEvent{}
	}
	if c.msgcopy.Clicked() {
		clipboard.WriteOp{Text: string(c.messageClicked.Plaintext)}.Add(gtx.Ops)
		c.messageClicked = nil
		return nil
	}
	if c.msgdetails.Clicked() {
		c.messageClicked = nil // not implemented
	}

	for msg, click := range c.messageClicks {
		for _, e := range click.Events(gtx.Queue) {
			if e.Type == gesture.TypeClick {
				c.messageClicked = msg
			}
		}
	}

	for _, e := range c.cancel.Events(gtx.Queue) {
		if e.Type == gesture.TypeClick {
			c.messageClicked = nil
		}
	}

	return nil
}

func layoutMessage(gtx C, msg *catshadow.Message, isSelected bool, expires time.Duration) D {

	var statusIcon *widget.Icon
	if msg.Outbound == true {
		statusIcon = queuedIcon
		switch {
		case !msg.Sent:
			statusIcon = queuedIcon
		case msg.Sent && !msg.Delivered:
			statusIcon = sentIcon
		case msg.Delivered:
			statusIcon = deliveredIcon
		default:
		}
	}

	return layout.Flex{Axis: layout.Vertical, Alignment: layout.End, Spacing: layout.SpaceBetween}.Layout(gtx,
		layout.Rigid(material.Body1(th, string(msg.Plaintext)).Layout),
		layout.Rigid(func(gtx C) D {
			in := layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(0), Left: unit.Dp(8), Right: unit.Dp(8)}
			return in.Layout(gtx, func(gtx C) D {
				timeLabel := strings.Replace(durafmt.ParseShort(time.Now().Round(0).Sub(msg.Timestamp).Truncate(time.Minute)).Format(units), "0 s", "now", 1)
				var whenExpires string
				if expires == 0 {
					whenExpires = ""
				} else {
					whenExpires = durafmt.ParseShort(msg.Timestamp.Add(expires).Sub(time.Now().Round(0).Truncate(time.Minute))).Format(units) + " remaining"
				}
				if isSelected {
					timeLabel = msg.Timestamp.Truncate(time.Minute).Format(time.RFC822)
					if msg.Outbound {
						timeLabel = "Sent: " + timeLabel
					} else {
						timeLabel = "Received: " + timeLabel
					}
				}
				if msg.Outbound {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End, Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Rigid(material.Caption(th, timeLabel).Layout),
						layout.Rigid(material.Caption(th, whenExpires).Layout),
						layout.Rigid(func(gtx C) D {
							return statusIcon.Layout(gtx, th.Palette.ContrastFg)
						}),
					)
					// do not show delivery status for received messages, instead show received timestamp
				} else {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End, Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Rigid(material.Caption(th, timeLabel).Layout),
						layout.Rigid(material.Caption(th, whenExpires).Layout),
					)
				}
			})
		}),
	)
}

func (c *conversationPage) Layout(gtx layout.Context) layout.Dimensions {
	contact := c.a.c.GetContacts()[c.nickname]
	if n, ok := notifications[c.nickname]; ok {
		if c.a.focus {
			n.Cancel()
			delete(notifications, c.nickname)
		}
	}
	messages := c.a.c.GetSortedConversation(c.nickname)
	expires, _ := c.a.c.GetExpiration(c.nickname)
	bgl := Background{
		Color: th.Bg,
		Inset: layout.Inset{Top: unit.Dp(0), Bottom: unit.Dp(0), Left: unit.Dp(0), Right: unit.Dp(0)},
	}

	return layout.Flex{Axis: layout.Vertical, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return bgl.Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(button(th, c.back, backIcon).Layout),
					layout.Rigid(func(gtx C) D {
						dims := layoutAvatar(gtx, c.a.c, c.nickname)
						a := clip.Rect(image.Rectangle{Max: dims.Size})
						t := a.Push(gtx.Ops)
						c.edit.Add(gtx.Ops)
						t.Pop()
						return dims
					}),
					layout.Rigid(material.Caption(th, c.nickname).Layout),
					layout.Rigid(func(gtx C) D {
						if contact.IsPending {
							return pandaIcon.Layout(gtx, th.Palette.ContrastFg)
						}
						return layout.Dimensions{}
					}),
					layout.Flexed(1, fill{th.Bg}.Layout),
				)
			},
			)
		}),
		layout.Flexed(2, func(gtx C) D {
			return bgl.Layout(gtx, func(ctx C) D {
				if len(messages) == 0 {
					return fill{th.Bg}.Layout(ctx)
				}

				dims := messageList.Layout(gtx, len(messages), func(gtx C, i int) layout.Dimensions {
					if _, ok := c.messageClicks[messages[i]]; !ok {
						c.messageClicks[messages[i]] = new(gesture.Click)
					}

					bgSender := Background{
						Color:  th.ContrastBg,
						Inset:  layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(8), Right: unit.Dp(12)},
						Radius: unit.Dp(10),
					}
					bgReceiver := Background{
						Color:  th.ContrastFg,
						Inset:  layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(12), Right: unit.Dp(8)},
						Radius: unit.Dp(10),
					}
					inbetween := layout.Inset{Top: unit.Dp(2)}
					if i > 0 {
						if messages[i-1].Outbound != messages[i].Outbound {
							inbetween = layout.Inset{Top: unit.Dp(8)}
						}
					}
					var dims D
					isSelected := messages[i] == c.messageClicked
					if messages[i].Outbound {
						dims = layout.Flex{Axis: layout.Horizontal, Alignment: layout.Baseline, Spacing: layout.SpaceAround}.Layout(gtx,
							layout.Flexed(1, fill{th.Bg}.Layout),
							layout.Flexed(5, func(gtx C) D {
								return inbetween.Layout(gtx, func(gtx C) D {
									return bgSender.Layout(gtx, func(gtx C) D {
										return layoutMessage(gtx, messages[i], isSelected, expires)
									})
								})
							}),
						)
					} else {
						dims = layout.Flex{Axis: layout.Horizontal, Alignment: layout.Baseline, Spacing: layout.SpaceAround}.Layout(gtx,
							layout.Flexed(5, func(gtx C) D {
								return inbetween.Layout(gtx, func(gtx C) D {
									return bgReceiver.Layout(gtx, func(gtx C) D {
										return layoutMessage(gtx, messages[i], isSelected, expires)
									})
								})
							}),
							layout.Flexed(1, fill{th.Bg}.Layout),
						)
					}
					a := clip.Rect(image.Rectangle{Max: dims.Size})
					t := a.Push(gtx.Ops)
					c.messageClicks[messages[i]].Add(gtx.Ops)
					t.Pop()
					return dims

				})
				if c.messageClicked != nil {
					a := clip.Rect(image.Rectangle{Max: dims.Size})
					t := a.Push(gtx.Ops)
					c.cancel.Add(gtx.Ops)
					t.Pop()
				}
				return dims
			})
		}),
		layout.Rigid(func(gtx C) D {
			bg := Background{
				Color: th.ContrastBg,
				Inset: layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(0), Left: unit.Dp(12), Right: unit.Dp(12)},
			}
			// return the menu laid out for message actions
			if c.messageClicked != nil {
				return bg.Layout(gtx, func(gtx C) D {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(material.Button(th, c.msgcopy, "copy").Layout),
						layout.Flexed(1, fill{th.Bg}.Layout),
						layout.Rigid(material.Button(th, c.msgdetails, "details").Layout),
					)
				})
			}
			bgSender := Background{
				Color:  th.ContrastBg,
				Inset:  layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(12), Right: unit.Dp(12)},
				Radius: unit.Dp(10),
			}
			bgl := Background{
				Color: th.Bg,
				Inset: layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(0), Left: unit.Dp(0), Right: unit.Dp(0)},
			}

			return bgl.Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, fill{th.Bg}.Layout),
					layout.Flexed(5, func(gtx C) D {
						dims := bgSender.Layout(gtx, material.Editor(th, c.compose, "").Layout)
						t := pointer.PassOp{}.Push(gtx.Ops)
						defer t.Pop()
						a := clip.Rect(image.Rectangle{Max: dims.Size})
						x := a.Push(gtx.Ops)
						defer x.Pop()
						c.msgpaste.Add(gtx.Ops)
						return dims
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, button(th, c.send, sendIcon).Layout)
					}),
				)
			})
		}),
	)
}

func newConversationPage(a *App, nickname string) *conversationPage {
	ed := &widget.Editor{SingleLine: false, Submit: true}
	if runtime.GOOS == "android" {
		ed.Submit = false
	}

	p := &conversationPage{a: a, nickname: nickname,
		compose:       ed,
		messageClicks: make(map[*catshadow.Message]*gesture.Click),
		back:          &widget.Clickable{},
		msgcopy:       &widget.Clickable{},
		msgpaste:      NewLongPress(a.w.Invalidate, 800*time.Millisecond),
		msgdetails:    &widget.Clickable{},
		cancel:        new(gesture.Click),
		send:          &widget.Clickable{},
		edit:          new(gesture.Click),
	}
	p.compose.Focus()
	return p
}
