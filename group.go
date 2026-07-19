package foxfire

import (
	"context"
	"fmt"
	"math"
)

// Room is a physical grouping of devices. A device belongs to at most one
// room. Its Services array contains the grouped_light through which the room
// is actuated -- the room itself is not directly writable.
type Room struct {
	ID       ID       `json:"id"`
	Type     string   `json:"type"`
	Metadata Metadata `json:"metadata"`
	Children []Ref    `json:"children"`
	Services []Ref    `json:"services"`
}

// GroupedLightID extracts the actuation service, which is what callers
// actually want ninety percent of the time.
func (r Room) GroupedLightID() (ID, bool) {
	for _, s := range r.Services {
		if s.RType == TypeGroupedLight {
			return s.RID, true
		}
	}
	return "", false
}

type RoomService struct{ c *Client }

func (s *RoomService) List(ctx context.Context) ([]Room, error) {
	return getMany[Room](ctx, s.c, "/resource/room")
}

func (s *RoomService) Get(ctx context.Context, id ID) (Room, error) {
	return getOne[Room](ctx, s.c, "/resource/room", id)
}

// ByName resolves a room by its user-facing label. Names are not unique or
// stable, so this is a convenience for scripts and the CLI, not something to
// build a daemon on.
func (s *RoomService) ByName(ctx context.Context, name string) (Room, error) {
	rooms, err := s.List(ctx)
	if err != nil {
		return Room{}, err
	}
	for _, r := range rooms {
		if r.Metadata.Name == name {
			return r, nil
		}
	}
	return Room{}, fmt.Errorf("%w: no room named %q", ErrNotFound, name)
}

// Zone is a logical grouping. Unlike a room, a light may belong to many
// zones, and zones group light services rather than devices.
type Zone struct {
	ID       ID       `json:"id"`
	Type     string   `json:"type"`
	Metadata Metadata `json:"metadata"`
	Children []Ref    `json:"children"`
	Services []Ref    `json:"services"`
}

func (z Zone) GroupedLightID() (ID, bool) {
	for _, s := range z.Services {
		if s.RType == TypeGroupedLight {
			return s.RID, true
		}
	}
	return "", false
}

type ZoneService struct{ c *Client }

func (s *ZoneService) List(ctx context.Context) ([]Zone, error) {
	return getMany[Zone](ctx, s.c, "/resource/zone")
}

func (s *ZoneService) Get(ctx context.Context, id ID) (Zone, error) {
	return getOne[Zone](ctx, s.c, "/resource/zone", id)
}

// ZoneCreate is the body for creating a zone. Children references the light
// services the zone groups -- note "light" services, not devices: a zone
// groups the actuation points, which is what lets one light belong to several
// zones. The bridge creates the zone's own grouped_light automatically.
type ZoneCreate struct {
	Metadata Metadata `json:"metadata"`
	Children []Ref    `json:"children"`
}

// Create makes a new zone and returns a reference to it. Configuration, not
// command traffic, so it is unthrottled.
//
// Note the asymmetry with reads: on creation the bridge *requires* an
// archetype, though it is optional when reading a zone back. Rather than make
// every caller remember that, an empty archetype defaults to "other".
func (s *ZoneService) Create(ctx context.Context, zc ZoneCreate) (Ref, error) {
	if zc.Metadata.Archetype == "" {
		zc.Metadata.Archetype = "other"
	}
	return post(ctx, s.c, "/resource/zone", zc, nil)
}

// Delete removes a zone. Its auto-created grouped_light goes with it.
func (s *ZoneService) Delete(ctx context.Context, id ID) error {
	return del(ctx, s.c, "/resource/zone", id)
}

// Scene is a stored set of per-light states scoped to a room or zone.
type Scene struct {
	ID       ID       `json:"id"`
	Type     string   `json:"type"`
	Metadata Metadata `json:"metadata"`
	Group    Ref      `json:"group"`
	Speed    float64  `json:"speed,omitempty"`
}

// RecallAction values. "active" applies the scene; "dynamic_palette" starts
// the scene cycling through its palette, which is the v2 headline feature.
const (
	RecallActive  = "active"
	RecallDynamic = "dynamic_palette"
	RecallStatic  = "static"
)

type sceneRecall struct {
	Recall struct {
		Action   string `json:"action"`
		Duration *int   `json:"duration,omitempty"`
	} `json:"recall"`
}

// SceneAction is one light's target state within a scene. The Target is the
// light service the state applies to; the fields mirror a LightUpdate but are
// stored rather than applied immediately. A scene must carry an action for
// every light it means to control -- a light with no action is left at
// whatever it was when the scene is recalled.
type SceneAction struct {
	Target Ref              `json:"target"`
	Action SceneTargetState `json:"action"`
}

type SceneTargetState struct {
	On               *On                     `json:"on,omitempty"`
	Dimming          *DimmingUpdate          `json:"dimming,omitempty"`
	Color            *ColorUpdate            `json:"color,omitempty"`
	ColorTemperature *ColorTemperatureUpdate `json:"color_temperature,omitempty"`
}

// SceneCreate is the body for creating a scene. Group must reference a room or
// zone; the bridge rejects a scene scoped to anything else. Actions holds the
// per-light target states.
type SceneCreate struct {
	Metadata Metadata      `json:"metadata"`
	Group    Ref           `json:"group"`
	Actions  []SceneAction `json:"actions"`
}

type SceneService struct{ c *Client }

func (s *SceneService) List(ctx context.Context) ([]Scene, error) {
	return getMany[Scene](ctx, s.c, "/resource/scene")
}

func (s *SceneService) Get(ctx context.Context, id ID) (Scene, error) {
	return getOne[Scene](ctx, s.c, "/resource/scene", id)
}

// Create stores a new scene and returns a reference to it. Creation is
// configuration, not command traffic, so it is not throttled against the
// light buckets. The group must be a room or zone; build actions with the
// same pointer helpers used for light updates.
func (s *SceneService) Create(ctx context.Context, sc SceneCreate) (Ref, error) {
	return post(ctx, s.c, "/resource/scene", sc, nil)
}

// Delete removes a scene.
func (s *SceneService) Delete(ctx context.Context, id ID) error {
	return del(ctx, s.c, "/resource/scene", id)
}

// Recall applies a scene. transitionMS of 0 uses the scene's stored duration.
// Recalls fan out like grouped-light commands, so they spend from that bucket.
func (s *SceneService) Recall(ctx context.Context, id ID, action string, transitionMS int) error {
	var body sceneRecall
	body.Recall.Action = action
	if transitionMS > 0 {
		body.Recall.Duration = Int(transitionMS)
	}
	return put(ctx, s.c, "/resource/scene", id, body, s.c.groupLim)
}

// Device is the physical unit. It owns one or more services (lights, sensors,
// connectivity) and carries the identifiers you need for support tickets.
type Device struct {
	ID          ID       `json:"id"`
	Type        string   `json:"type"`
	Metadata    Metadata `json:"metadata"`
	Services    []Ref    `json:"services"`
	ProductData struct {
		ModelID          string `json:"model_id"`
		ManufacturerName string `json:"manufacturer_name"`
		ProductName      string `json:"product_name"`
		SoftwareVersion  string `json:"software_version"`
		Certified        bool   `json:"certified"`
	} `json:"product_data"`
}

type DeviceService struct{ c *Client }

func (s *DeviceService) List(ctx context.Context) ([]Device, error) {
	return getMany[Device](ctx, s.c, "/resource/device")
}

func (s *DeviceService) Get(ctx context.Context, id ID) (Device, error) {
	return getOne[Device](ctx, s.c, "/resource/device", id)
}

// ByName resolves a device by its user-facing label. First match wins; names
// are neither unique nor stable, so this is a convenience, not a key.
func (s *DeviceService) ByName(ctx context.Context, name string) (Device, error) {
	devices, err := s.List(ctx)
	if err != nil {
		return Device{}, err
	}
	for _, d := range devices {
		if d.Metadata.Name == name {
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("%w: no device named %q", ErrNotFound, name)
}

// Rename changes a device's user-facing name. This is a metadata edit, not a
// command, so it passes nil for the limiter: renames must not spend from the
// light or grouped-light buckets, which exist to protect against dropping
// actual light commands.
func (s *DeviceService) Rename(ctx context.Context, id ID, name string) error {
	body := struct {
		Metadata Metadata `json:"metadata"`
	}{Metadata: Metadata{Name: name}}
	return put(ctx, s.c, "/resource/device", id, body, nil)
}

// Motion is a presence sensor service. Enabled is separately controllable
// from Motion.Motion, and a disabled sensor reports stale values rather than
// an error, which is worth guarding against.
type Motion struct {
	ID      ID     `json:"id"`
	Type    string `json:"type"`
	Owner   Ref    `json:"owner"`
	Enabled bool   `json:"enabled"`
	Motion  struct {
		Motion       bool `json:"motion"`
		MotionValid  bool `json:"motion_valid"`
		MotionReport *struct {
			Changed string `json:"changed"`
			Motion  bool   `json:"motion"`
		} `json:"motion_report,omitempty"`
	} `json:"motion"`
}

type MotionService struct{ c *Client }

func (s *MotionService) List(ctx context.Context) ([]Motion, error) {
	return getMany[Motion](ctx, s.c, "/resource/motion")
}

func (s *MotionService) Get(ctx context.Context, id ID) (Motion, error) {
	return getOne[Motion](ctx, s.c, "/resource/motion", id)
}

// GroupedMotion aggregates the motion state of every sensor in a group,
// owned by a bridge_home rather than a device. Newer bridges create one
// automatically. Note the shape differs from Motion: there is no top-level
// motion or motion_valid, only the report, so a caller reads the last reported
// value and its timestamp. Changed is RFC 3339 and may be empty before the
// first report.
type GroupedMotion struct {
	ID      ID     `json:"id"`
	Type    string `json:"type"`
	Owner   Ref    `json:"owner"`
	Enabled bool   `json:"enabled"`
	Motion  struct {
		MotionReport *struct {
			Changed string `json:"changed"`
			Motion  bool   `json:"motion"`
		} `json:"motion_report,omitempty"`
	} `json:"motion"`
}

// Detected reports whether the group last saw motion, and whether a reading
// has been reported at all. A group with no report yet returns (false, false).
func (g GroupedMotion) Detected() (motion, reported bool) {
	if g.Motion.MotionReport == nil {
		return false, false
	}
	return g.Motion.MotionReport.Motion, true
}

type GroupedMotionService struct{ c *Client }

func (s *GroupedMotionService) List(ctx context.Context) ([]GroupedMotion, error) {
	return getMany[GroupedMotion](ctx, s.c, "/resource/grouped_motion")
}

func (s *GroupedMotionService) Get(ctx context.Context, id ID) (GroupedMotion, error) {
	return getOne[GroupedMotion](ctx, s.c, "/resource/grouped_motion", id)
}

// Button is one physical button on a switch or dimmer. A four-button dimmer
// presents four button services owned by one device, told apart by
// Metadata.ControlID (1-based). The interesting data is transient and arrives
// on the event stream as a ButtonReport; a plain GET mostly tells you a button
// exists and what events it can emit. LastEvent is one of initial_press,
// repeat, short_release, long_press, long_release.
type Button struct {
	ID       ID     `json:"id"`
	Type     string `json:"type"`
	Owner    Ref    `json:"owner"`
	Metadata struct {
		ControlID int `json:"control_id"`
	} `json:"metadata"`
	Button struct {
		LastEvent      string `json:"last_event,omitempty"`
		RepeatInterval int    `json:"repeat_interval,omitempty"`
		ButtonReport   *struct {
			Updated string `json:"updated"`
			Event   string `json:"event"`
		} `json:"button_report,omitempty"`
		EventValues []string `json:"event_values,omitempty"`
	} `json:"button"`
}

type ButtonService struct{ c *Client }

func (s *ButtonService) List(ctx context.Context) ([]Button, error) {
	return getMany[Button](ctx, s.c, "/resource/button")
}

func (s *ButtonService) Get(ctx context.Context, id ID) (Button, error) {
	return getOne[Button](ctx, s.c, "/resource/button", id)
}

// Temperature reports in Celsius, and only from mains-powered or recently
// woken battery sensors.
type Temperature struct {
	ID          ID     `json:"id"`
	Type        string `json:"type"`
	Owner       Ref    `json:"owner"`
	Enabled     bool   `json:"enabled"`
	Temperature struct {
		Temperature      float64 `json:"temperature"`
		TemperatureValid bool    `json:"temperature_valid"`
	} `json:"temperature"`
}

type TemperatureService struct{ c *Client }

func (s *TemperatureService) List(ctx context.Context) ([]Temperature, error) {
	return getMany[Temperature](ctx, s.c, "/resource/temperature")
}

func (s *TemperatureService) Get(ctx context.Context, id ID) (Temperature, error) {
	return getOne[Temperature](ctx, s.c, "/resource/temperature", id)
}

// LightLevel is an ambient-light sensor service. The raw value is not lux: the
// bridge reports 10000*log10(lux)+1, so lux = 10^((level-1)/10000). LightValid
// distinguishes a real reading from a stale one on a disabled or sleeping
// sensor, and must be checked -- a disabled sensor reports its last value, not
// zero and not an error.
type LightLevel struct {
	ID      ID     `json:"id"`
	Type    string `json:"type"`
	Owner   Ref    `json:"owner"`
	Enabled bool   `json:"enabled"`
	Light   struct {
		LightLevel      int  `json:"light_level"`
		LightLevelValid bool `json:"light_level_valid"`
	} `json:"light"`
}

// Lux converts the bridge's logarithmic light_level to approximate lux. The
// reading is only meaningful when Light.LightLevelValid is true.
func (l LightLevel) Lux() float64 {
	return math.Pow(10, float64(l.Light.LightLevel-1)/10000)
}

type LightLevelService struct{ c *Client }

func (s *LightLevelService) List(ctx context.Context) ([]LightLevel, error) {
	return getMany[LightLevel](ctx, s.c, "/resource/light_level")
}

func (s *LightLevelService) Get(ctx context.Context, id ID) (LightLevel, error) {
	return getOne[LightLevel](ctx, s.c, "/resource/light_level", id)
}

// DevicePower reports the battery state of a battery-powered device. Battery
// level is a percentage; state is one of "normal", "low", or "critical". A
// mains-powered device has no device_power service at all, so the absence of
// one is not an error.
type DevicePower struct {
	ID         ID     `json:"id"`
	Type       string `json:"type"`
	Owner      Ref    `json:"owner"`
	PowerState struct {
		BatteryState string `json:"battery_state"`
		BatteryLevel int    `json:"battery_level"`
	} `json:"power_state"`
}

type DevicePowerService struct{ c *Client }

func (s *DevicePowerService) List(ctx context.Context) ([]DevicePower, error) {
	return getMany[DevicePower](ctx, s.c, "/resource/device_power")
}

func (s *DevicePowerService) Get(ctx context.Context, id ID) (DevicePower, error) {
	return getOne[DevicePower](ctx, s.c, "/resource/device_power", id)
}
