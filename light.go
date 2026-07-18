package foxfire

import "context"

// On is the power state sub-resource.
type On struct {
	On bool `json:"on"`
}

// Dimming carries brightness as a percentage in (0, 100]. Note that zero is
// not "off": the bridge clamps to MinDimLevel, and turning a light off is
// done through On.
type Dimming struct {
	Brightness  float64  `json:"brightness"`
	MinDimLevel *float64 `json:"min_dim_level,omitempty"`
}

// XY is a CIE 1931 chromaticity coordinate. The bridge speaks xy natively;
// RGB and HSV conversions belong in the caller or in a helper package, not
// in the wire types.
type XY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Gamut describes the color volume a particular light can actually reproduce.
// Sending an xy outside the gamut is not an error; the light silently clamps
// to the nearest reproducible point, which is a common source of "the color
// is wrong" bug reports.
type Gamut struct {
	Red   XY `json:"red"`
	Green XY `json:"green"`
	Blue  XY `json:"blue"`
}

type Color struct {
	XY        XY     `json:"xy"`
	Gamut     *Gamut `json:"gamut,omitempty"`
	GamutType string `json:"gamut_type,omitempty"`
}

// ColorTemperature is expressed in mireds, not kelvin. Mirek = 1e6 / kelvin.
type ColorTemperature struct {
	Mirek      int  `json:"mirek"`
	MirekValid bool `json:"mirek_valid"`
	Schema     *struct {
		MirekMinimum int `json:"mirek_minimum"`
		MirekMaximum int `json:"mirek_maximum"`
	} `json:"mirek_schema,omitempty"`
}

// Light is a single controllable light service. It is owned by a device;
// a multi-head fixture presents several lights owned by one device.
type Light struct {
	ID       ID       `json:"id"`
	IDv1     string   `json:"id_v1,omitempty"`
	Type     string   `json:"type"`
	Owner    Ref      `json:"owner"`
	Metadata Metadata `json:"metadata"`

	On               On                `json:"on"`
	Dimming          *Dimming          `json:"dimming,omitempty"`
	Color            *Color            `json:"color,omitempty"`
	ColorTemperature *ColorTemperature `json:"color_temperature,omitempty"`

	Mode string `json:"mode,omitempty"`
}

// Name is a convenience for the common case of wanting the user-facing label.
func (l Light) Name() string { return l.Metadata.Name }

// LightUpdate is a partial update. Every field is optional; absent fields are
// left untouched by the bridge. Construct with the Bool/Float helpers:
//
//	LightUpdate{On: &On{On: true}, Dimming: &DimmingUpdate{Brightness: Float(40)}}
type LightUpdate struct {
	On               *On                     `json:"on,omitempty"`
	Dimming          *DimmingUpdate          `json:"dimming,omitempty"`
	Color            *ColorUpdate            `json:"color,omitempty"`
	ColorTemperature *ColorTemperatureUpdate `json:"color_temperature,omitempty"`
	Dynamics         *Dynamics               `json:"dynamics,omitempty"`
	Alert            *Alert                  `json:"alert,omitempty"`
}

type DimmingUpdate struct {
	Brightness *float64 `json:"brightness,omitempty"`
}

type ColorUpdate struct {
	XY *XY `json:"xy,omitempty"`
}

type ColorTemperatureUpdate struct {
	Mirek *int `json:"mirek,omitempty"`
}

// Dynamics controls the transition into the requested state. Duration is in
// milliseconds and is the single most useful field in the whole API: without
// it, every change is an abrupt step.
type Dynamics struct {
	Duration *int `json:"duration,omitempty"`
}

// Alert triggers the identify behaviour, which is how you find out which
// physical bulb a UUID corresponds to.
type Alert struct {
	Action string `json:"action"` // "breathe"
}

// LightService is the /resource/light endpoint.
type LightService struct{ c *Client }

func (s *LightService) List(ctx context.Context) ([]Light, error) {
	return getMany[Light](ctx, s.c, "/resource/light")
}

func (s *LightService) Get(ctx context.Context, id ID) (Light, error) {
	return getOne[Light](ctx, s.c, "/resource/light", id)
}

// Update applies a partial change, spending a token from the per-light bucket.
func (s *LightService) Update(ctx context.Context, id ID, u LightUpdate) error {
	return put(ctx, s.c, "/resource/light", id, u, s.c.lightLim)
}

// SetOn is the shorthand everyone writes on day one.
func (s *LightService) SetOn(ctx context.Context, id ID, on bool) error {
	return s.Update(ctx, id, LightUpdate{On: &On{On: on}})
}

// SetBrightness sets brightness as a percentage, optionally fading over
// transition milliseconds. Pass 0 for an immediate change.
func (s *LightService) SetBrightness(ctx context.Context, id ID, pct float64, transitionMS int) error {
	u := LightUpdate{Dimming: &DimmingUpdate{Brightness: Float(pct)}}
	if transitionMS > 0 {
		u.Dynamics = &Dynamics{Duration: Int(transitionMS)}
	}
	return s.Update(ctx, id, u)
}

// Identify makes the light breathe so a human can pick it out of a ceiling.
func (s *LightService) Identify(ctx context.Context, id ID) error {
	return s.Update(ctx, id, LightUpdate{Alert: &Alert{Action: "breathe"}})
}

// GroupedLight is the actuation service for a room or zone. Writing to it is
// dramatically cheaper than iterating members, because the bridge issues a
// Zigbee multicast rather than N unicasts.
type GroupedLight struct {
	ID      ID       `json:"id"`
	Type    string   `json:"type"`
	Owner   Ref      `json:"owner"`
	On      On       `json:"on"`
	Dimming *Dimming `json:"dimming,omitempty"`
}

type GroupedLightService struct{ c *Client }

func (s *GroupedLightService) List(ctx context.Context) ([]GroupedLight, error) {
	return getMany[GroupedLight](ctx, s.c, "/resource/grouped_light")
}

func (s *GroupedLightService) Get(ctx context.Context, id ID) (GroupedLight, error) {
	return getOne[GroupedLight](ctx, s.c, "/resource/grouped_light", id)
}

// Update spends from the grouped-light bucket, which is an order of magnitude
// tighter than the per-light one.
func (s *GroupedLightService) Update(ctx context.Context, id ID, u LightUpdate) error {
	return put(ctx, s.c, "/resource/grouped_light", id, u, s.c.groupLim)
}

func (s *GroupedLightService) SetOn(ctx context.Context, id ID, on bool) error {
	return s.Update(ctx, id, LightUpdate{On: &On{On: on}})
}
