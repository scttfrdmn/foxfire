// Command foxfire is a CLI over the library: discover and pair a bridge, list
// and control lights, rooms, and devices, read sensors, rename devices, and
// stream events. It doubles as the exercise harness against real hardware --
// if the CLI can pair, list, control, and watch, the library works.
//
// Listing commands accept a global --json flag for machine-readable output.
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
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/scttfrdmn/foxfire"
	"github.com/scttfrdmn/foxfire/color"
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

// asJSON is set by the global --json flag. It is read by the listing commands,
// which emit a JSON array instead of a table so the CLI composes with jq and
// scripts rather than only human eyes.
var asJSON bool

func run(args []string) error {
	// Pull the global --json flag out of the argument list wherever it appears,
	// so it works both before and after the subcommand.
	args = extractFlags(args)

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
		return cmdPair(ctx, args[1:])
	case "lights":
		return cmdLights(ctx)
	case "rooms":
		return cmdRooms(ctx)
	case "on", "off":
		return cmdSwitch(ctx, args)
	case "set":
		return cmdSet(ctx, args[1:])
	case "bridge":
		return cmdBridge(ctx)
	case "sensors":
		return cmdSensors(ctx)
	case "devices":
		return cmdDevices(ctx)
	case "rename":
		return cmdRename(ctx, args[1:])
	case "watch":
		return cmdWatch(ctx)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q; try 'foxfire help'", args[0])
	}
}

// extractFlags removes recognised global flags from args and records them,
// returning the remaining positional arguments.
func extractFlags(args []string) []string {
	out := args[:0]
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			out = append(out, a)
		}
	}
	return out
}

// emitJSON writes v as indented JSON. Listing commands call this when --json
// is set; keeping it in one place keeps the output shape consistent.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func usage() {
	fmt.Print(`foxfire - Philips Hue CLIP v2 client

  discover          find bridges via mDNS, falling back to cloud discovery
  pair [id|ip]      press the link button, then run this; pass a bridge
                    ID or IP to choose when more than one is present
  lights            list lights
  rooms             list rooms
  on   <room|light>   turn a room or light on
  off  <room|light>   turn a room or light off
  set  <light> bri <0-100> | color <#hex>   set brightness or color
  bridge            show bridge id, firmware, and zigbee status
  sensors           show motion, light, temperature, and battery readings
  devices           list paired devices
  rename <old> <new>  rename a device
  watch             stream events until interrupted

Global flags:
  --json            emit machine-readable JSON from listing commands

Credentials are stored under your OS config dir, mode 0600:
  Linux    ~/.config/foxfire/credentials.json
  macOS    ~/Library/Application Support/foxfire/credentials.json
  Windows  %AppData%\foxfire\credentials.json
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

func cmdPair(ctx context.Context, args []string) error {
	bridges, err := foxfire.Discover(ctx, 5*time.Second)
	if err != nil {
		return err
	}
	if len(bridges) == 0 {
		return fmt.Errorf("no bridges discovered")
	}

	// Selecting bridges[0] is unsafe when more than one bridge is on the
	// network -- discovery order is not stable, so a blind pair can pin the
	// wrong hub. Require an explicit selector (bridge ID or IP) whenever the
	// choice is ambiguous.
	var b foxfire.Bridge
	sel := strings.ToLower(strings.TrimSpace(strings.Join(args, "")))
	switch {
	case sel != "":
		match := -1
		for i, cand := range bridges {
			if strings.ToLower(cand.ID) == sel || cand.Addr == sel {
				match = i
				break
			}
		}
		if match < 0 {
			return fmt.Errorf("no discovered bridge matches %q", sel)
		}
		b = bridges[match]
	case len(bridges) == 1:
		b = bridges[0]
	default:
		var lines []string
		for _, cand := range bridges {
			lines = append(lines, fmt.Sprintf("  %s  %s", cand.ID, cand.Addr))
		}
		return fmt.Errorf("%d bridges found; specify one by ID or IP:\n%s",
			len(bridges), strings.Join(lines, "\n"))
	}

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
	if asJSON {
		return emitJSON(lights)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tSTATE\tBRIGHTNESS\tID")
	for _, l := range lights {
		state := "off"
		if l.On.On {
			state = "on"
		}
		// A plug is a light with no dimming; show a dash rather than a
		// meaningless brightness, and label the type so it is not mistaken
		// for a dimmable bulb.
		kind := "light"
		if l.IsPlug() {
			kind = "plug"
		}
		bri := "-"
		if l.Dimming != nil && !l.IsPlug() {
			bri = fmt.Sprintf("%.0f%%", l.Dimming.Brightness)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", l.Name(), kind, state, bri, l.ID)
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
	if asJSON {
		return emitJSON(rooms)
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
		return fmt.Errorf("usage: foxfire %s <room|light>", args[0])
	}
	on := args[0] == "on"
	name := strings.Join(args[1:], " ")

	c, err := client()
	if err != nil {
		return err
	}

	// Prefer a room: writing a room's grouped light is a single Zigbee
	// multicast, far cheaper than touching members individually. Fall back to
	// an individual light by name, so a hub with lights but no rooms -- a
	// freshly paired bulb or strip -- is still controllable.
	room, err := c.Rooms.ByName(ctx, name)
	if err == nil {
		gid, ok := room.GroupedLightID()
		if !ok {
			return fmt.Errorf("room %q has no grouped light service", name)
		}
		return c.GroupedLights.SetOn(ctx, gid, on)
	}
	if !errors.Is(err, foxfire.ErrNotFound) {
		return err
	}

	light, lerr := c.Lights.ByName(ctx, name)
	if lerr != nil {
		if errors.Is(lerr, foxfire.ErrNotFound) {
			return fmt.Errorf("no room or light named %q", name)
		}
		return lerr
	}
	return c.Lights.SetOn(ctx, light.ID, on)
}

// cmdSet controls brightness and color of an individual light by name:
//
//	foxfire set "Hue lightstrip 1" bri 40
//	foxfire set "Hue lightstrip 1" color #ff8800
//
// It targets a single light rather than a room because color and brightness
// are per-light concerns; the on/off command is the one that fans out to a
// room's grouped light.
func cmdSet(ctx context.Context, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf(`usage: foxfire set "<light>" bri <0-100> | color <#hex>`)
	}
	name, attr, value := args[0], args[1], args[2]

	c, err := client()
	if err != nil {
		return err
	}
	light, err := c.Lights.ByName(ctx, name)
	if err != nil {
		return err
	}

	switch attr {
	case "bri", "brightness":
		pct, perr := strconv.ParseFloat(value, 64)
		if perr != nil || pct < 0 || pct > 100 {
			return fmt.Errorf("brightness must be a number 0-100, got %q", value)
		}
		// A one-second fade reads as intentional rather than abrupt.
		return c.Lights.SetBrightness(ctx, light.ID, pct, 1000)
	case "color":
		if light.Color == nil {
			return fmt.Errorf("light %q does not support color", name)
		}
		rgb, cerr := color.Hex(value)
		if cerr != nil {
			return cerr
		}
		// Clamp to the light's own gamut so the requested color is the one that
		// lands, rather than whatever the bridge silently moves it to.
		var cu *foxfire.ColorUpdate
		if light.Color.Gamut != nil {
			cu = color.UpdateInGamut(rgb, *light.Color.Gamut)
		} else {
			cu = color.Update(rgb)
		}
		return c.Lights.Update(ctx, light.ID, foxfire.LightUpdate{
			Color:    cu,
			Dynamics: &foxfire.Dynamics{Duration: foxfire.Int(1000)},
		})
	default:
		return fmt.Errorf("unknown attribute %q; use bri or color", attr)
	}
}

func cmdBridge(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	b, err := c.Bridge.Self(ctx)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Bridge ID\t%s\n", b.BridgeID)
	tz := b.TimeZone.TimeZone
	if tz == "" {
		tz = "(unset)"
	}
	fmt.Fprintf(w, "Time zone\t%s\n", tz)

	// Firmware lives on the owning device, not the bridge resource itself.
	if b.Owner.RType == foxfire.TypeDevice {
		if dev, err := c.Devices.Get(ctx, b.Owner.RID); err == nil {
			fmt.Fprintf(w, "Model\t%s\n", dev.ProductData.ModelID)
			fmt.Fprintf(w, "Firmware\t%s\n", dev.ProductData.SoftwareVersion)
		}
	}

	if zs, err := c.Zigbee.List(ctx); err == nil {
		for _, z := range zs {
			reach := "unreachable"
			if z.Reachable() {
				reach = "connected"
			}
			fmt.Fprintf(w, "Zigbee\t%s (%s)\n", reach, z.MACAddress)
		}
	}
	return w.Flush()
}

func cmdSensors(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tREADING\tID")

	if ms, err := c.Motion.List(ctx); err == nil {
		for _, m := range ms {
			v := "invalid"
			if m.Motion.MotionValid {
				v = fmt.Sprintf("motion=%v", m.Motion.Motion)
			}
			fmt.Fprintf(w, "motion\t%s\t%s\n", v, m.ID)
		}
	}
	if ls, err := c.LightLevel.List(ctx); err == nil {
		for _, l := range ls {
			v := "invalid"
			if l.Light.LightLevelValid {
				v = fmt.Sprintf("%d (~%.0f lux)", l.Light.LightLevel, l.Lux())
			}
			fmt.Fprintf(w, "light_level\t%s\t%s\n", v, l.ID)
		}
	}
	if ts, err := c.Temperature.List(ctx); err == nil {
		for _, t := range ts {
			v := "invalid"
			if t.Temperature.TemperatureValid {
				v = fmt.Sprintf("%.1f C", t.Temperature.Temperature)
			}
			fmt.Fprintf(w, "temperature\t%s\t%s\n", v, t.ID)
		}
	}
	if ps, err := c.DevicePower.List(ctx); err == nil {
		for _, p := range ps {
			fmt.Fprintf(w, "device_power\t%d%% (%s)\t%s\n",
				p.PowerState.BatteryLevel, p.PowerState.BatteryState, p.ID)
		}
	}
	return w.Flush()
}

func cmdDevices(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	devices, err := c.Devices.List(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON(devices)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPRODUCT\tMODEL\tID")
	for _, d := range devices {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			d.Metadata.Name, d.ProductData.ProductName, d.ProductData.ModelID, d.ID)
	}
	return w.Flush()
}

func cmdRename(ctx context.Context, args []string) error {
	// Names routinely contain spaces ("Hue lightstrip 1"), so require exactly
	// two arguments and let the shell's quoting delimit them rather than trying
	// to guess where the old name ends and the new one begins.
	if len(args) != 2 {
		return fmt.Errorf(`usage: foxfire rename "<old name>" "<new name>"`)
	}
	oldName, newName := args[0], args[1]

	c, err := client()
	if err != nil {
		return err
	}
	dev, err := c.Devices.ByName(ctx, oldName)
	if err != nil {
		return err
	}
	if err := c.Devices.Rename(ctx, dev.ID, newName); err != nil {
		return err
	}
	fmt.Printf("Renamed %q to %q\n", oldName, newName)
	return nil
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
