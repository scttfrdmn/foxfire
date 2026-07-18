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

type SceneService struct{ c *Client }

func (s *SceneService) List(ctx context.Context) ([]Scene, error) {
	return getMany[Scene](ctx, s.c, "/resource/scene")
}

func (s *SceneService) Get(ctx context.Context, id ID) (Scene, error) {
	return getOne[Scene](ctx, s.c, "/resource/scene", id)
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
