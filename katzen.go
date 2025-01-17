package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/katzenpost/katzenpost/catshadow"
	"github.com/katzenpost/katzenpost/client"
	"time"

	"gioui.org/app"
	_ "gioui.org/app/permission/foreground"
	_ "gioui.org/app/permission/storage"
	"gioui.org/font/gofont"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"gioui.org/x/notify"
	"net/http"
	_ "net/http/pprof"
)

const (
	initialPKIConsensusTimeout = 45 * time.Second
	notificationTimeout        = 30 * time.Second
)

var (
	dataDirName      = "catshadow"
	clientConfigFile = flag.String("f", "", "Path to the client config file.")
	stateFile        = flag.String("s", "catshadow_statefile", "Path to the client state file.")
	debug            = flag.Bool("d", false, "Enable golang debug service.")

	minPasswordLen = 5 // XXX pick something reasonable

	notifications = make(map[string]notify.Notification)

	//go:embed default_config_without_tor.toml
	cfgWithoutTor []byte
	//go:embed default_config_with_tor.toml
	cfgWithTor []byte

	// theme
	th = func() *material.Theme {
		th := material.NewTheme(gofont.Collection())
		th.Bg = rgb(0x0)
		th.Fg = rgb(0xFFFFFFFF)
		th.ContrastBg = rgb(0x22222222)
		th.ContrastFg = rgb(0x77777777)
		return th
	}()

	isConnected  bool
	isConnecting bool
)

type App struct {
	endBg func()
	w     *app.Window
	ops   *op.Ops
	c     *catshadow.Client
	stack pageStack
	focus bool
	stage system.Stage
}

func newApp(w *app.Window) *App {
	a := &App{
		w:   w,
		ops: &op.Ops{},
	}
	return a
}

func (a *App) Layout(gtx layout.Context) {
	a.update(gtx)
	a.stack.Current().Layout(gtx)
}

func (a *App) update(gtx layout.Context) {
	page := a.stack.Current()
	if e := page.Event(gtx); e != nil {
		switch e := e.(type) {
		case RedrawEvent:
			a.w.Invalidate()
		case BackEvent:
			a.stack.Pop()
		case signInStarted:
			p := newUnlockPage(e.result)
			a.stack.Clear(p)
		case unlockError:
			isConnected = false
			isConnecting = false
			fmt.Printf("unlockError: %s\n", e.err)
			a.stack.Clear(newSignInPage(a))
		case restartClient:
			isConnected = false
			isConnecting = false
			fmt.Printf("restartClient\n")
			a.stack.Clear(newSignInPage(a))
		case unlockSuccess:
			// validate the statefile somehow
			a.c = e.client
			a.c.Start()
			a.stack.Clear(newHomePage(a))
			if _, err := a.c.GetBlob("AutoConnect"); err == nil {
				a.c.Online()
				isConnecting = true
				// if the client does not already have a spool
				// descriptor, prompt to create one
				spool := a.c.SpoolWriteDescriptor()
				if spool == nil {
					a.stack.Push(newSpoolPage(a))
				}
			}
		case OfflineClick:
			go a.c.Offline()
			isConnected = false
			isConnecting = false
		case OnlineClick:
			go a.c.Online()
			isConnecting = true
			spool := a.c.SpoolWriteDescriptor()
			if spool == nil {
				a.stack.Push(newSpoolPage(a))
			}
		case ShowSettingsClick:
			a.stack.Push(newSettingsPage(a))
		case AddContactClick:
			a.stack.Push(newAddContactPage(a))
		case AddContactComplete:
			a.stack.Pop()
		case ChooseContactClick:
			a.stack.Push(newConversationPage(a, e.nickname))
		case ChooseAvatar:
			a.stack.Push(newAvatarPicker(a, e.nickname, ""))
		case ChooseAvatarPath:
			a.stack.Pop()
			a.stack.Push(newAvatarPicker(a, e.nickname, e.path))
		case RenameContact:
			a.stack.Push(newRenameContactPage(a, e.nickname))
		case EditContact:
			a.stack.Push(newEditContactPage(a, e.nickname))
		case EditContactComplete:
			a.stack.Clear(newHomePage(a))
		case MessageSent:
		}
	}
}

func (a *App) run() error {
	// only read window events until client is established
	for {
		if a.c != nil {
			break
		}
		e := <-a.w.Events()
		if err := a.handleGioEvents(e); err != nil {
			return err
		}
	}
	defer func() {
		if a.c != nil {
			a.c.Shutdown()
			a.c.Wait()
		}
	}()

	// select from all event sources
	for {
		select {
		case e := <-a.c.EventSink:
			if err := a.handleCatshadowEvent(e); err != nil {
				return err
			}
		case e := <-a.w.Events():
			if err := a.handleGioEvents(e); err != nil {
				return err
			}
		case <-time.After(1 * time.Minute):
			// redraw the screen to update the message timestamps once per minute
			a.w.Invalidate()
		}
	}
}

func main() {
	flag.Parse()
	fmt.Println("Katzenpost is still pre-alpha.  DO NOT DEPEND ON IT FOR STRONG SECURITY OR ANONYMITY.")

	if *debug {
		go func() {
			http.ListenAndServe("localhost:8080", nil)
		}()
		runtime.SetMutexProfileFraction(1)
		runtime.SetBlockProfileRate(1)
	}

	// Start graphical user interface.
	uiMain()
}

func uiMain() {
	go func() {
		w := app.NewWindow(
			app.Size(unit.Dp(400), unit.Dp(400)),
			app.Title("Katzen"),
			app.NavigationColor(rgb(0x0)),
			app.StatusColor(rgb(0x0)),
		)
		if err := newApp(w).run(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed: %v\n", err)
		}
		os.Exit(0)
	}()
	app.Main()
}

type (
	C = layout.Context
	D = layout.Dimensions
)

func (a *App) handleCatshadowEvent(e interface{}) error {
	switch event := e.(type) {
	case *client.ConnectionStatusEvent:
		isConnecting = false
		if event.IsConnected {
			isConnected = true
			go func() {
				if n, err := notify.Push("Connected", "Katzen has connected"); err == nil {
					<-time.After(notificationTimeout)
					n.Cancel()
				}
			}()
		} else {
			isConnected = false
			go func() {
				if n, err := notify.Push("Disconnected", "Katzen has disconnected"); err == nil {
					<-time.After(notificationTimeout)
					n.Cancel()
				}
			}()
		}
		if event.Err != nil {
			go func() {
				if n, err := notify.Push("Error", fmt.Sprintf("Katzen error: %s", event.Err)); err == nil {
					<-time.After(notificationTimeout)
					n.Cancel()
				}
			}()
		}
	case *catshadow.KeyExchangeCompletedEvent:
		if event.Err != nil {
			if n, err := notify.Push("Key Exchange", fmt.Sprintf("Failed: %s", event.Err)); err == nil {
				go func() { <-time.After(notificationTimeout); n.Cancel() }()
			}
		} else {
			if n, err := notify.Push("Key Exchange", fmt.Sprintf("Completed: %s", event.Nickname)); err == nil {
				go func() { <-time.After(notificationTimeout); n.Cancel() }()
			}
		}
	case *catshadow.MessageNotSentEvent:
		if n, err := notify.Push("Message Not Sent", fmt.Sprintf("Failed to send message to %s", event.Nickname)); err == nil {
			go func() { <-time.After(notificationTimeout); n.Cancel() }()
		}
	case *catshadow.MessageReceivedEvent:
		// do not notify for the focused conversation
		p := a.stack.Current()
		switch p := p.(type) {
		case *conversationPage:
			// XXX: on android, input focus is not lost when the application does not have foreground
			// but system.Stage is changed. On desktop linux, the stage does not change, but window focus is lost.
			if p.nickname == event.Nickname && a.stage == system.StageRunning && a.focus {
				a.w.Invalidate()
				return nil
			}
		}
		// emit a notification in all other cases
		if n, err := notify.Push("Message Received", fmt.Sprintf("Message Received from %s", event.Nickname)); err == nil {
			if o, ok := notifications[event.Nickname]; ok {
				// cancel old notification before replacing with a new one
				o.Cancel()
			}
			notifications[event.Nickname] = n
		}
	case *catshadow.MessageSentEvent:
	case *catshadow.MessageDeliveredEvent:
	default:
		// do not invalidate window for events we do not care about
		return nil
	}
	// redraw the screen when an event we care about is received
	a.w.Invalidate()
	return nil
}

func (a *App) handleGioEvents(e interface{}) error {
	switch e := e.(type) {
	case key.FocusEvent:
		a.focus = e.Focus
	case system.DestroyEvent:
		return errors.New("system.DestroyEvent receieved")
	case system.FrameEvent:
		gtx := layout.NewContext(a.ops, e)
		key.InputOp{Tag: a.w, Keys: key.NameEscape + "|" + key.NameBack}.Add(a.ops)
		for _, e := range gtx.Events(a.w) {
			switch e := e.(type) {
			case key.Event:
				if (e.Name == key.NameEscape && e.State == key.Release) || e.Name == key.NameBack {
					if a.stack.Len() > 1 {
						a.stack.Pop()
						a.w.Invalidate()
					}
				}
			}
		}
		a.Layout(gtx)
		e.Frame(gtx.Ops)
	case system.StageEvent:
		fmt.Printf("StageEvent %s received\n", e.Stage)
		a.stage = e.Stage
		if e.Stage >= system.StageRunning {
			if a.stack.Len() == 0 {
				a.stack.Push(newSignInPage(a))
			}
		}
		if e.Stage == system.StagePaused {
			var err error
			a.endBg, err = app.Start("Is running in the background", "")
			if err != nil {
				return err
			}
		} else {
			if a.endBg != nil {
				a.endBg()
			}
		}
	}
	return nil
}
