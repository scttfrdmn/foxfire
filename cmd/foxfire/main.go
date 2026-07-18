// Command foxfire is a thin CLI over the library. It exists mostly as a
// smoke test against real hardware -- if the CLI can pair, list, and watch,
// the library works.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/scttfrdmn/foxfire"
)

type stored struct {
	Addr        string `json:"addr"`
	BridgeID    string `json:"bridge_id"`
	AppKey      string `json:"app_key"`
	ClientKey   string `json:"client_key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "foxfire:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "discover":
		return cmdDiscover(ctx)
	case "pair":
		return cmdPair(ctx)
	case "lights":
		return cmdLights(ctx)
	case "rooms":
		return cmdRooms(ctx)
	case "on", "off":
		return cmdSwitch(ctx, args)
	case "watch":
		return cmdWatch(ctx)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q; try 'foxfire help'", args[0])
	}
}

func usage() {
	fmt.Print(`foxfire - Philips Hue CLIP v2 client

  discover          find bridges via mDNS, falling back to cloud discovery
  pair              press the link button, then run this
  lights            list lights
  rooms             list rooms
  on   <room>       turn a room on
  off  <room>       turn a room off
  watch             stream events until interrupted

Credentials are stored in ~/.config/foxfire/credentials.json.
`)
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "foxfire", "credentials.json"), nil
}

func load() (stored, error) {
	var s stored
	p, err := configPath()
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, fmt.Errorf("no credentials; run 'foxfire pair' first")
		}
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func save(s stored) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// 0600: this file is a bearer token for every light in the house.
	return os.WriteFile(p, b, 0o600)
}

func client() (*foxfire.Client, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	opts := []foxfire.Option{foxfire.WithBridgeID(s.BridgeID)}
	if s.Fingerprint != "" {
		if sum, err := hex.DecodeString(s.Fingerprint); err == nil {
			opts = append(opts, foxfire.WithPinnedFingerprint(sum))
		}
	}
	return foxfire.New(s.Addr, s.AppKey, opts...)
}

func cmdDiscover(ctx context.Context) error {
	bridges, err := foxfire.Discover(ctx, 5*time.Second)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tBRIDGE ID\tVIA")
	for _, b := range bridges {
		fmt.Fprintf(w, "%s\t%s\t%s\n", b.Addr, b.ID, b.Source)
	}
	return w.Flush()
}

func cmdPair(ctx context.Context) error {
	bridges, err := foxfire.Discover(ctx, 5*time.Second)
	if err != nil {
		return err
	}
	b := bridges[0]
	if b.ID == "" {
		return fmt.Errorf("bridge at %s advertised no bridge ID; cannot pin its certificate", b.Addr)
	}
	fmt.Printf("Found bridge %s at %s\n", b.ID, b.Addr)

	// Record the certificate now, on the assumption that the network is
	// trustworthy at pairing time, and pin it forever after.
	fp, err := foxfire.PeerFingerprint(b.Addr)
	if err != nil {
		return err
	}
	fmt.Printf("Certificate SHA-256: %s\n", hex.EncodeToString(fp))

	host, _ := os.Hostname()
	fmt.Println("Press the link button on the bridge now...")

	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	creds, err := foxfire.PairWait(waitCtx, b.Addr, "foxfire", host,
		foxfire.WithPinnedFingerprint(fp))
	if err != nil {
		return err
	}

	if err := save(stored{
		Addr:        b.Addr,
		BridgeID:    b.ID,
		AppKey:      creds.ApplicationKey,
		ClientKey:   creds.ClientKey,
		Fingerprint: hex.EncodeToString(fp),
	}); err != nil {
		return err
	}
	fmt.Println("Paired.")
	return nil
}

func cmdLights(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	lights, err := c.Lights.List(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tBRIGHTNESS\tID")
	for _, l := range lights {
		state := "off"
		if l.On.On {
			state = "on"
		}
		bri := "-"
		if l.Dimming != nil {
			bri = fmt.Sprintf("%.0f%%", l.Dimming.Brightness)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Name(), state, bri, l.ID)
	}
	return w.Flush()
}

func cmdRooms(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	rooms, err := c.Rooms.List(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDEVICES\tID")
	for _, r := range rooms {
		fmt.Fprintf(w, "%s\t%d\t%s\n", r.Metadata.Name, len(r.Children), r.ID)
	}
	return w.Flush()
}

func cmdSwitch(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: foxfire %s <room>", args[0])
	}
	on := args[0] == "on"
	name := strings.Join(args[1:], " ")

	c, err := client()
	if err != nil {
		return err
	}
	room, err := c.Rooms.ByName(ctx, name)
	if err != nil {
		return err
	}
	gid, ok := room.GroupedLightID()
	if !ok {
		return fmt.Errorf("room %q has no grouped light service", name)
	}
	return c.GroupedLights.SetOn(ctx, gid, on)
}

func cmdWatch(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	batches, errs := c.Subscribe(ctx)
	fmt.Println("Watching. Ctrl-C to stop.")
	for {
		select {
		case <-ctx.Done():
			return nil
		case b, ok := <-batches:
			if !ok {
				return nil
			}
			for _, ev := range b.Events {
				fmt.Printf("%s  %-14s %s", b.CreationTime.Format(time.TimeOnly), ev.Type, ev.ID)
				if ev.On != nil {
					fmt.Printf("  on=%v", ev.On.On)
				}
				if ev.Dimming != nil {
					fmt.Printf("  bri=%.0f", ev.Dimming.Brightness)
				}
				if ev.Motion != nil {
					fmt.Printf("  motion=%v", ev.Motion.Motion)
				}
				if ev.Button != nil {
					fmt.Printf("  button=%s", ev.Button.LastEvent)
				}
				fmt.Println()
			}
		case err, ok := <-errs:
			if ok && err != nil {
				fmt.Fprintln(os.Stderr, "stream:", err)
			}
		}
	}
}
