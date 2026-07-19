package foxfire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newFakeBridge stands up a TLS server speaking enough CLIP v2 to exercise
// the client. Using httptest's own certificate and transport sidesteps the
// bridge-identity machinery, which is tested separately.
func newFakeBridge(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)

	c, err := New(srv.Listener.Addr().String(), "test-key",
		WithTransport(srv.Client().Transport),
		WithRateLimits(1000, 1000)) // do not make tests wait on token buckets
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestListLights(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(keyHeader); got != "test-key" {
			t.Errorf("application key header = %q, want test-key", got)
		}
		if r.URL.Path != "/clip/v2/resource/light" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"errors":[],"data":[
			{"id":"abc","type":"light","metadata":{"name":"Desk"},
			 "on":{"on":true},"dimming":{"brightness":42.5}}]}`)
	}))

	lights, err := c.Lights.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(lights) != 1 {
		t.Fatalf("got %d lights, want 1", len(lights))
	}
	if lights[0].Name() != "Desk" || !lights[0].On.On {
		t.Errorf("unexpected light: %+v", lights[0])
	}
	if lights[0].Dimming == nil || lights[0].Dimming.Brightness != 42.5 {
		t.Errorf("brightness not decoded: %+v", lights[0].Dimming)
	}
}

// The bridge returns 200 with a populated errors array for partially applied
// updates. Treating that as success is the bug this test exists to prevent.
func TestErrorsArrayOnSuccessStatus(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"errors":[{"description":"device (light) is not powered"}],"data":[]}`)
	}))

	err := c.Lights.SetOn(context.Background(), "abc", true)
	if err == nil {
		t.Fatal("expected an error for a populated errors array")
	}
	if !strings.Contains(err.Error(), "not powered") {
		t.Errorf("error did not carry the description: %v", err)
	}
}

func TestUnauthorizedMapsToSentinel(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"description":"unauthorized user"}],"data":[]}`)
	}))

	_, err := c.Lights.List(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// Partial-update semantics: an update that sets only brightness must not
// serialize an "on" field, because doing so would command a state change the
// caller never asked for.
func TestPartialUpdateOmitsUnsetFields(t *testing.T) {
	var body []byte
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"errors":[],"data":[]}`)
	}))

	if err := c.Lights.SetBrightness(context.Background(), "abc", 30, 400); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("request body was not JSON: %v", err)
	}
	if _, present := decoded["on"]; present {
		t.Errorf("brightness-only update sent an 'on' field: %s", body)
	}
	if _, present := decoded["dimming"]; !present {
		t.Errorf("update omitted dimming: %s", body)
	}
	if dyn, ok := decoded["dynamics"].(map[string]any); !ok || dyn["duration"] != float64(400) {
		t.Errorf("transition not encoded: %s", body)
	}
}

// Brightness zero is a real command, not an absent field. This is the exact
// case pointer-valued update fields exist to disambiguate.
func TestZeroBrightnessIsSent(t *testing.T) {
	var body []byte
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"errors":[],"data":[]}`)
	}))

	if err := c.Lights.SetBrightness(context.Background(), "abc", 0, 0); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	if !strings.Contains(string(body), `"brightness":0`) {
		t.Errorf("zero brightness was dropped: %s", body)
	}
}

func TestRoomGroupedLightID(t *testing.T) {
	r := Room{Services: []Ref{
		{RID: "svc-1", RType: TypeZigbeeConnect},
		{RID: "svc-2", RType: TypeGroupedLight},
	}}
	id, ok := r.GroupedLightID()
	if !ok || id != "svc-2" {
		t.Errorf("got (%q, %v), want (svc-2, true)", id, ok)
	}

	if _, ok := (Room{}).GroupedLightID(); ok {
		t.Error("empty room reported a grouped light")
	}
}

func TestSubscribeDecodesBatches(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != eventStreamPath {
			t.Errorf("stream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ": hi\n\n")
		fmt.Fprint(w, "id: 1\ndata: [{\"creationtime\":\"2026-07-18T10:00:00Z\",\"type\":\"update\","+
			"\"data\":[{\"id\":\"abc\",\"type\":\"light\",\"on\":{\"on\":false}}]}]\n\n")
		w.(http.Flusher).Flush()
		// Leave the connection open briefly so the reader is not racing a
		// close, then let the handler return to end the stream.
		time.Sleep(100 * time.Millisecond)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches, _ := c.Subscribe(ctx)
	select {
	case b := <-batches:
		if b.Type != "update" || len(b.Events) != 1 {
			t.Fatalf("unexpected batch: %+v", b)
		}
		ev := b.Events[0]
		if ev.ID != "abc" || ev.On == nil || ev.On.On {
			t.Errorf("unexpected event: %+v", ev)
		}
		if ev.Dimming != nil {
			t.Error("event carried a dimming field the bridge did not send")
		}
	case <-ctx.Done():
		t.Fatal("no batch received")
	}
}

// A button switch presents several button services owned by one device, told
// apart by control_id. Validating the decode here rather than on hardware: no
// button device is available, and the value that matters (the press) arrives
// on the stream, tested separately below.
func TestListButtons(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/clip/v2/resource/button" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"errors":[],"data":[
			{"id":"btn-1","type":"button","owner":{"rid":"dev","rtype":"device"},
			 "metadata":{"control_id":1},
			 "button":{"last_event":"short_release","event_values":["initial_press","short_release"]}},
			{"id":"btn-2","type":"button","owner":{"rid":"dev","rtype":"device"},
			 "metadata":{"control_id":2},"button":{}}]}`)
	}))

	btns, err := c.Buttons.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(btns) != 2 {
		t.Fatalf("got %d buttons, want 2", len(btns))
	}
	if btns[0].Metadata.ControlID != 1 || btns[0].Button.LastEvent != "short_release" {
		t.Errorf("button 1 decoded wrong: %+v", btns[0])
	}
	if len(btns[0].Button.EventValues) != 2 {
		t.Errorf("event_values not decoded: %+v", btns[0].Button.EventValues)
	}
	if btns[1].Metadata.ControlID != 2 {
		t.Errorf("button 2 control_id wrong: %+v", btns[1])
	}
}

// Button presses arrive on the event stream as a ButtonReport, not from a GET.
// This is the path a caller actually watches, so it is the one that must decode.
func TestButtonEventDecodes(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [{\"creationtime\":\"2026-07-18T10:00:00Z\",\"type\":\"update\","+
			"\"data\":[{\"id\":\"btn-1\",\"type\":\"button\","+
			"\"button\":{\"last_event\":\"initial_press\"}}]}]\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches, _ := c.Subscribe(ctx)
	select {
	case b := <-batches:
		if len(b.Events) != 1 || b.Events[0].Button == nil {
			t.Fatalf("button event not decoded: %+v", b)
		}
		if b.Events[0].Button.LastEvent != "initial_press" {
			t.Errorf("last_event = %q, want initial_press", b.Events[0].Button.LastEvent)
		}
	case <-ctx.Done():
		t.Fatal("no batch received")
	}
}

// Scene creation goes through POST and returns the bridge-assigned reference
// in the update envelope. This checks the method, the body, and that the
// returned Ref is decoded -- and that Zone.Create fills in the archetype the
// bridge requires on create but treats as optional on read.
func TestSceneAndZoneCreate(t *testing.T) {
	var sceneBody, zoneBody []byte
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		switch r.URL.Path {
		case "/clip/v2/resource/zone":
			zoneBody, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"errors":[],"data":[{"rid":"zone-1","rtype":"zone"}]}`)
		case "/clip/v2/resource/scene":
			sceneBody, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"errors":[],"data":[{"rid":"scene-1","rtype":"scene"}]}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))

	zref, err := c.Zones.Create(context.Background(), ZoneCreate{
		Metadata: Metadata{Name: "Z"},
		Children: []Ref{{RID: "light-1", RType: TypeLight}},
	})
	if err != nil {
		t.Fatalf("Zone.Create: %v", err)
	}
	if zref.RID != "zone-1" || zref.RType != TypeZone {
		t.Errorf("zone ref = %+v", zref)
	}
	// The archetype the read shape omits must be supplied on create.
	var zm map[string]any
	_ = json.Unmarshal(zoneBody, &zm)
	if md, ok := zm["metadata"].(map[string]any); !ok || md["archetype"] != "other" {
		t.Errorf("zone create did not default archetype: %s", zoneBody)
	}

	sref, err := c.Scenes.Create(context.Background(), SceneCreate{
		Metadata: Metadata{Name: "S"},
		Group:    Ref{RID: "zone-1", RType: TypeZone},
		Actions: []SceneAction{{
			Target: Ref{RID: "light-1", RType: TypeLight},
			Action: SceneTargetState{On: &On{On: true}},
		}},
	})
	if err != nil {
		t.Fatalf("Scene.Create: %v", err)
	}
	if sref.RID != "scene-1" {
		t.Errorf("scene ref = %+v", sref)
	}
	if !strings.Contains(string(sceneBody), `"actions"`) {
		t.Errorf("scene body missing actions: %s", sceneBody)
	}
}

// A create that reports no error but returns no reference is a bridge contract
// violation, and post must surface it rather than return a zero Ref that reads
// as a valid ID.
func TestCreateWithNoReferenceErrors(t *testing.T) {
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"errors":[],"data":[]}`)
	}))
	_, err := c.Zones.Create(context.Background(), ZoneCreate{Metadata: Metadata{Name: "Z"}})
	if err == nil {
		t.Fatal("expected an error when create returns no reference")
	}
}

// Rename is a metadata edit, not a command. It must PUT only the name, and it
// must not send any of the state fields that would command a change.
func TestDeviceRename(t *testing.T) {
	var body []byte
	var method, path string
	c, _ := newFakeBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"errors":[],"data":[{"rid":"dev-1","rtype":"device"}]}`)
	}))

	if err := c.Devices.Rename(context.Background(), "dev-1", "Kitchen"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if method != http.MethodPut || path != "/clip/v2/resource/device/dev-1" {
		t.Errorf("got %s %s, want PUT /clip/v2/resource/device/dev-1", method, path)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	md, ok := decoded["metadata"].(map[string]any)
	if !ok || md["name"] != "Kitchen" {
		t.Errorf("rename body did not carry the name: %s", body)
	}
	if _, present := decoded["on"]; present {
		t.Errorf("rename sent an 'on' field: %s", body)
	}
}

func TestNewRequiresExplicitTLSPosture(t *testing.T) {
	if _, err := New("192.0.2.1", "key"); err == nil {
		t.Fatal("expected New to refuse an unconfigured trust posture")
	}
	if _, err := New("192.0.2.1", "key", WithInsecureTLS()); err != nil {
		t.Fatalf("explicit insecure should be accepted: %v", err)
	}
}

func TestBackoffIsBoundedAndJittered(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		d := backoff(3)
		if d <= 0 || d > 30*time.Second {
			t.Fatalf("backoff(3) = %v, out of bounds", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Error("backoff produced no jitter")
	}
	if got := backoff(100); got > 30*time.Second {
		t.Errorf("backoff(100) = %v, exceeds cap", got)
	}
}
