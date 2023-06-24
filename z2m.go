package hapz2m

import (
	"encoding/json"
	"fmt"
	"regexp"

	"reflect"
	"runtime"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

const Z2M_DEBUG = false

// exposed properties to ignore
var IgnoreProperties = map[string]bool{
	"linkquality":        true,
	"device_temperature": true,
}

var (
	ErrDeviceSkipped  = fmt.Errorf("device is skipped")
	ErrMalfomedDevice = fmt.Errorf("device is malformed")

	ErrNotNumericCharacteristic = fmt.Errorf("characteristic is non-numeric")
)

// Creates a HAP Accessory from a Z2M Device
// It may return ErrDeviceSkipped if the device is not supported or still being interviewed.
func createAccessory(dev *Device) (*accessory.A, []*ExposeMapping, error) {
	if dev.Disabled ||
		!dev.Supported ||
		!dev.InterviewCompleted {
		return nil, nil, ErrDeviceSkipped
	}

	if dev.Definition == nil {
		return nil, nil, ErrMalfomedDevice
	}

	// check that network address is not zero
	// we use this for accessory ID
	if dev.NetworkAddress == 0 {
		return nil, nil, ErrMalfomedDevice
	}

	if Z2M_DEBUG {
		fmt.Printf("%s %s %d\n", dev.Definition.Model, dev.Definition.Vendor, dev.NetworkAddress)
		for _, exp := range dev.Definition.Exposes {
			ignored := ""
			if exp.Ignored() {
				ignored = " [ignored]"
			}

			fmt.Printf(" - %s: %d %s %s%s\n", exp.Type, exp.Access, exp.Name, exp.Property, ignored)
			if exp.Type == "enum" {
				fmt.Printf("   %+v\n", exp.Values)
			}
		}
	}

	// remove "0x" prefix of address for serial number
	serialNum := dev.IEEEAddress
	if len(serialNum) > 5 && serialNum[0] == '0' && serialNum[1] == 'x' {
		serialNum = serialNum[2:]
	}

	// create accessory first, then see if any handler wants to modify it
	acc := accessory.New(accessory.Info{
		Name:         dev.Definition.Description,
		SerialNumber: serialNum,
		Manufacturer: dev.Manufacturer,
		Model:        dev.Definition.Model,
		Firmware:     dev.SoftwareBuildId,
	}, accessory.TypeUnknown)

	// use network address as accessory ID
	acc.Id = uint64(dev.NetworkAddress)

	var guessedAccTypes []byte
	var allSvcs []*service.S
	var allExposes []*ExposeMapping

	for _, createFunc := range createServiceHandlers {
		accType, svcs, exposes, err := createFunc(dev)
		if err != nil {
			panic(err)
		}

		if Z2M_DEBUG {
			f := runtime.FuncForPC(reflect.ValueOf(createFunc).Pointer())
			fmt.Printf("----- %s -----\n", f.Name())

			if accType != accessory.TypeUnknown {
				fmt.Printf("typ: %#v\n", accType)
			}
			fmt.Print("svc: [")
			for _, s := range svcs {
				fmt.Printf("%+v", s)
			}
			fmt.Println("]")

			fmt.Print("exp: [")
			for _, e := range exposes {
				fmt.Printf("%+v", e)
			}
			fmt.Println("]")
		}

		if accType != accessory.TypeUnknown && len(svcs) > 0 && len(exposes) > 0 {
			guessedAccTypes = append(guessedAccTypes, accType)
		}

		allSvcs = append(allSvcs, svcs...)
		allExposes = append(allExposes, exposes...)
	}

	// copy value ranges
	for _, e := range allExposes {
		if e.ExposesEntry.Type != "numeric" {
			continue
		}

		// if it's a percentage, then don't copy
		if e.Characteristic.Unit == characteristic.UnitPercentage {
			// assign a PercentageTranslator here, if there wasn't already
			if e.Translator == nil && e.ExposesEntry.IsSettable() &&
				e.ExposesEntry.ValueMin != nil && e.ExposesEntry.ValueMax != nil {

				e.Translator = &PercentageTranslator{*e.ExposesEntry.ValueMin, *e.ExposesEntry.ValueMax}
			}
			continue
		}

		err := e.ExposesEntry.CopyValueRanges(e.Characteristic)
		if err != nil {
			return nil, nil, fmt.Errorf("cant copy value ranges for %s to cfmt %s: %s",
				e.ExposesEntry.Property, e.Characteristic.Type, err)
		}
	}

	// guess main accessory type
	// XXX in case of tie?
	uniqAccTypes := uniq(guessedAccTypes)
	if len(uniqAccTypes) == 1 {
		acc.Type = uniqAccTypes[0]
	}

	// add services to accessory
	for _, s := range allSvcs {
		acc.AddS(s)
	}

	return acc, allExposes, nil
}

var (
	accPermsFormattingRE = regexp.MustCompile(`(?isU)"perms":\s*\[.*\]`)
	accPermsWhitespaceRE = regexp.MustCompile(`( \[|,)?\s*(\S)(\])?`)
)

// Dumps the Accessory structure
func dumpAccessory(acc *accessory.A) error {
	s, err := json.MarshalIndent(acc, "", "  ")
	if err != nil {
		return err
	}

	// performs some formatting to keep "perms" on a single-line to reduce space
	s = accPermsFormattingRE.ReplaceAllFunc(s, func(s []byte) []byte {
		return accPermsWhitespaceRE.ReplaceAll(s, []byte("$1$2$3"))
	})

	fmt.Println(string(s))
	return nil
}

// Returns unique items from the slice
func uniq[T comparable](items []T) []T {
	m := make(map[T]bool)
	for _, e := range items {
		m[e] = true
	}
	u := make([]T, len(m))
	i := 0
	for k := range m {
		u[i] = k
		i++
	}
	return u
}

// Implements a translator between the exposed property and Characteristic
type MappingTranslator interface {
	ToCharacteristicValue(exposedValue any) (cValue any, err error)
	ToExposedValue(cValue any) (exposedValue any, err error)
}

// Default pass-through "translator", where both exposed and Characteristic
// values are of the same or similar types.
type PassthruTranslator struct{}

var defaultTranslator = &PassthruTranslator{}

func (p *PassthruTranslator) ToExposedValue(v any) (any, error)        { return v, nil }
func (p *PassthruTranslator) ToCharacteristicValue(v any) (any, error) { return v, nil }

var ErrTranslationError = fmt.Errorf("cannot translate value")

// Translates a "binary" type exposed value to arbitrary Characteristic values
type BoolTranslator struct{ TrueValue, FalseValue any }

func (t *BoolTranslator) ToExposedValue(cVal any) (any, error) {
	switch cVal {
	case t.TrueValue:
		return true, nil

	case t.FalseValue:
		return false, nil
	}
	return nil, ErrTranslationError
}

func (t *BoolTranslator) ToCharacteristicValue(eVal any) (any, error) {
	bVal, ok := eVal.(bool)
	if !ok {
		return nil, ErrTranslationError
	} else if bVal {
		return t.TrueValue, nil
	}
	return t.FalseValue, nil
}

// Translates a numeric type exposed value to percentage Characteristic values
type PercentageTranslator struct{ Min, Max float64 }

func (t *PercentageTranslator) ToExposedValue(cVal any) (any, error) {
	cVal2, ok := valToFloat64(cVal)
	if !ok {
		return nil, ErrTranslationError
	}
	v := t.Min + (cVal2 / 100. * (t.Max - t.Min))
	return v, nil
}

func (t *PercentageTranslator) ToCharacteristicValue(eVal any) (any, error) {
	eVal2, ok := valToFloat64(eVal)
	if !ok {
		return nil, ErrTranslationError
	}
	v := (eVal2 - t.Min) * 100. / (t.Max - t.Min)
	return v, nil
}

// Converts numeric values to float64, if possible
// Returns the converted float64 value and a bool indicating if it was successful.
func valToFloat64(v any) (float64, bool) {
	val := reflect.ValueOf(v)
	switch {
	case val.CanInt():
		return float64(val.Int()), true
	case val.CanUint():
		return float64(val.Uint()), true
	case val.CanFloat():
		return val.Float(), true
	}
	return 0, false
}

//////////////////////////////

// Maps a zigbee2mqtt device property into a HAP characteristic.
// Contains convenience methods to translate values between the two systems.
// z2m "binary" types are inherently translated to bool using the ValueOn/Off properties defined,
// so no translator is required for that.
// A MappingTranslator is required if the values are not pass-through (like float to float, or float to int).
// An example is the ContactSensor, where HAP defines contact as [0, 1] instead of a bool ("binary").
// The BoolTranslator assists with this by providing true & false values that correspond to a bool.
type ExposeMapping struct {
	ExposesEntry   *ExposesEntry
	Characteristic *characteristic.C

	Translator MappingTranslator
}

func (m *ExposeMapping) String() string {
	return fmt.Sprintf("{%q,%s -> ctyp %s}",
		m.ExposesEntry.Name, m.ExposesEntry.Type, m.Characteristic.Type)
}

// Converts a Characteristic value to its corresponding z2m value
func (m *ExposeMapping) ToExposedValue(v any) (any, error) {
	t := m.Translator
	if t == nil {
		t = defaultTranslator
	}

	expVal, err := t.ToExposedValue(v)
	if err != nil {
		return expVal, err
	}

	// additional mapping is required for "binary" types to ValueOn/Off
	if m.ExposesEntry.Type == "binary" {
		b, isbool := expVal.(bool)
		if !isbool {
			return expVal, fmt.Errorf("translated value for binary type is not bool: %[1]T %[1]v", expVal)
		}

		expVal = m.ExposesEntry.ValueOff
		if b {
			expVal = m.ExposesEntry.ValueOn
		}
	}
	return expVal, err
}

// Calls c.SetValueRequest() with the translated exposed value
// if the error code is -1, there was a translation error.
// Otherwise it's a HAP error code
func (m *ExposeMapping) SetCharacteristicValue(v any) (any, int) {
	t := m.Translator
	if t == nil {
		t = defaultTranslator
	}

	// mapping for "binary" types to bool
	if m.ExposesEntry.Type == "binary" {
		switch v {
		case m.ExposesEntry.ValueOn:
			v = true
		case m.ExposesEntry.ValueOff:
			v = false
		default:
			return v, -1
		}
	}

	cv, err := t.ToCharacteristicValue(v)
	if err != nil {
		return v, -1
	}

	return m.Characteristic.SetValueRequest(cv, nil)
}

//////////////////////////////

// Function that creates services from a z2m Device, invoked by createAccessory()
// These functions are registered using RegisterCreateServiceHandler()
type CreateServiceFunc func(dev *Device) (byte, []*service.S, []*ExposeMapping, error)

// Registers a CreateServiceFunc for use by createAccessory()
func RegisterCreateServiceHandler(f CreateServiceFunc) {
	createServiceHandlers = append(createServiceHandlers, f)
}

// registered createService handlers
var createServiceHandlers []CreateServiceFunc

//////////////////////////////

type Device struct {
	FriendlyName       string `json:"friendly_name"`
	IEEEAddress        string `json:"ieee_address"`
	InterviewCompleted bool   `json:"interview_completed"`
	Interviewing       bool   `json:"interviewing"`
	Manufacturer       string `json:"manufacturer"`
	ModelId            string `json:"model_id"`
	NetworkAddress     int    `json:"network_address"`
	PowerSource        string `json:"power_source"`
	SoftwareBuildId    string `json:"software_build_id"`
	DateCode           string `json:"date_code"`

	Definition *DevDefinition `json:"definition"`

	Disabled  bool   `json:"disabled"`
	Supported bool   `json:"supported"`
	Type      string `json:"type"`
}

type DevDefinition struct {
	Description string         `json:"description"`
	Model       string         `json:"model"`
	Vendor      string         `json:"vendor"`
	Exposes     []ExposesEntry `json:"exposes"`
}

type ExposesEntry struct {
	Access      int    `json:"access"`
	Description string `json:"description"`
	Name        string `json:"name"`
	Property    string `json:"property"`
	Type        string `json:"type"`
	Unit        string `json:"unit"`
	Endpoint    string `json:"endpoint"`

	Features []ExposesEntry `json:"features"`

	// values
	Values []any `json:"values"`

	ValueOn  any `json:"value_on"`
	ValueOff any `json:"value_off"`

	ValueMax  *float64 `json:"value_max"`
	ValueMin  *float64 `json:"value_min"`
	ValueStep *float64 `json:"value_step"`
}

func (e *ExposesEntry) Ignored() bool { return IgnoreProperties[e.Name] }

// https://github.com/Koenkk/zigbee-herdsman-converters/blob/v15.0.0/lib/exposes.js#L458-L486
func (e *ExposesEntry) IsStateSetGet() bool { return e.Access == 0b111 }
func (e *ExposesEntry) IsSettable() bool    { return e.Access&0b10 == 0b10 }

// Updates MinVal, MaxVal and StepVal for the Characteristic
func (e *ExposesEntry) CopyValueRanges(c *characteristic.C) error {
	// ensure characteristic has a numeric type
	switch c.Format {
	case characteristic.FormatFloat, characteristic.FormatUInt8,
		characteristic.FormatUInt16, characteristic.FormatUInt32,
		characteristic.FormatUInt64, characteristic.FormatInt32:
		break

	default:
		return ErrNotNumericCharacteristic
	}

	if err := copyToCVal(e.ValueMin, &c.MinVal, c.Format); err != nil {
		return err
	}

	if err := copyToCVal(e.ValueMax, &c.MaxVal, c.Format); err != nil {
		return err
	}

	if err := copyToCVal(e.ValueStep, &c.StepVal, c.Format); err != nil {
		return err
	}

	return nil
}

func floatToCVal(v float64, cfmt string) (any, error) {
	switch cfmt {
	case characteristic.FormatFloat:
		return v, nil

	case characteristic.FormatInt32:
		return int(v), nil

	case characteristic.FormatUInt8, characteristic.FormatUInt16,
		characteristic.FormatUInt32, characteristic.FormatUInt64:
		if v < 0 {
			return 0, fmt.Errorf("cant convert %v to %s", v, cfmt)
		}
		return int(v), nil

	default:
		return 0, fmt.Errorf("unknown C value type %s\n", cfmt)
	}
}

func copyToCVal(v *float64, targetCVal *any, cfmt string) error {
	if v == nil {
		return nil
	}

	cv, err := floatToCVal(*v, cfmt)
	if err == nil {
		*targetCVal = cv
	}

	return err
}
