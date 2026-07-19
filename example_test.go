package foxfire_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/scttfrdmn/foxfire"
	"github.com/scttfrdmn/foxfire/color"
)

// Discover a bridge, then pair with it. Pairing requires a human to press the
// link button on the bridge; PairWait polls until they do or the context is
// cancelled.
func Example_pairing() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bridges, err := foxfire.Discover(ctx, 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	b := bridges[0]

	// Record the certificate on a network you trust, then pin it forever after.
	fp, err := foxfire.PeerFingerprint(b.Addr)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Press the link button on the bridge...")
	creds, err := foxfire.PairWait(ctx, b.Addr, "myapp", "hostname",
		foxfire.WithPinnedFingerprint(fp))
	if err != nil {
		log.Fatal(err)
	}
	_ = creds.ApplicationKey // store this; it is a bearer token for the bridge
}

// Once paired, construct a client and list the lights. New refuses to build
// without an explicit TLS posture, so one of WithBridgeID, WithRootCA,
// WithPinnedFingerprint, or WithInsecureTLS is required.
func Example_listLights() {
	c, err := foxfire.New("192.168.1.42", "your-application-key",
		foxfire.WithBridgeID("001788fffe1234ab"))
	if err != nil {
		log.Fatal(err)
	}

	lights, err := c.Lights.List(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, l := range lights {
		fmt.Printf("%s: on=%v\n", l.Name(), l.On.On)
	}
}

// Set brightness with a fade, and set color from a hex string. Color is
// clamped to the light's own gamut so the requested color is the one that
// lands rather than whatever the bridge silently moves it to.
func ExampleLightService_SetBrightness() {
	c, _ := foxfire.New("192.168.1.42", "key", foxfire.WithInsecureTLS())
	ctx := context.Background()

	light, err := c.Lights.ByName(ctx, "Desk")
	if err != nil {
		log.Fatal(err)
	}

	// Fade to 40% over one second.
	if err := c.Lights.SetBrightness(ctx, light.ID, 40, 1000); err != nil {
		log.Fatal(err)
	}

	// Turn it warm orange.
	rgb, _ := color.Hex("#ff8800")
	update := foxfire.LightUpdate{Color: color.Update(rgb)}
	if light.Color != nil && light.Color.Gamut != nil {
		update.Color = color.UpdateInGamut(rgb, *light.Color.Gamut)
	}
	if err := c.Lights.Update(ctx, light.ID, update); err != nil {
		log.Fatal(err)
	}
}

// Subscribe to the event stream and print motion events as they arrive. The
// subscription reconnects internally; the error channel is informational and
// safe to ignore for a script that only cares about events.
func ExampleClient_Subscribe() {
	c, _ := foxfire.New("192.168.1.42", "key", foxfire.WithInsecureTLS())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batches, _ := c.Subscribe(ctx)
	for batch := range batches {
		for _, ev := range batch.Events {
			if ev.Motion != nil {
				fmt.Printf("motion on %s: %v\n", ev.ID, ev.Motion.Motion)
			}
		}
	}
}
