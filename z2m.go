package hapz2m

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"reflect"
	"runtime"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

// show more messages for developers
const Z2M_DEVMODE = false

// exposed properties to ignore
var IgnoreProperties = map[string]bool{
	"linkquality":        true,
	"device_temperature": true,
}

var (
	ErrDeviceSkipped     = fmt.Errorf("device is skipped")
	ErrMalfomedDevice    = fmt.Errorf("device is malformed")
	ErrUnknownDeviceType = fmt.Errorf("device type is unknown")

	ErrNotNumericCharacteristic = fmt.Errorf("characteristic is non-numeric")
)

// Wire up the ExposeMappings and translators, where necessary
func initExposeMappings(exposes ...*ExposeMapping) error {
	for _, e := range exposes {
		// assign the default translator if none was specified
		if e.Translator == nil {
			e.Translator = defaultTranslator
		}

		// chain a translator for ValueOn/Off translations
		if e.ExposesEntry.Type == "binary" {
			bt := &BoolTranslator{e.ExposesEntry.ValueOn, e.ExposesEntry.ValueOff}

			// add the BoolTranslator for the exposed value
			e.Translator = &ChainedTranslator{CharacteristicSide: e.Translator,
				ExposedSide: &FlippedTranslator{bt}}
		}

		if e.ExposesEntry.Type != "numeric" {
			continue
		}

		// if it's a percentage, then don't copy
		if e.Characteristic.Unit == characteristic.UnitPercentage {
			// assign a PercentageTranslator here, if there wasn't already
			if e.Translator != defaultTranslator && e.ExposesEntry.IsSettable() &&
				e.ExposesEntry.ValueMin != nil && e.ExposesEntry.ValueMax != nil {

				e.Translator = &PercentageTranslator{*e.ExposesEntry.ValueMin, *e.ExposesEntry.ValueMax}
			}
			continue
		}

		err := e.ExposesEntry.CopyValueRanges(e.Characteristic)
		if err != nil {
			return fmt.Errorf("cant copy value ranges for %s to cfmt %s: %s",
				e.ExposesEntry.Property, e.Characteristic.Type, err)
		}
	}
	return nil
}

// Creates a HAP Accessory from a Z2M Device
// It may return ErrDeviceSkipped if the device is not supported or still being interviewed.
func createAccessory(dev *Device) (*accessory.A, []*ExposeMapping, error) {
	if dev.Disabled ||
		!dev.Supported ||
		!dev.InterviewCompleted ||
		dev.Type != "EndDevice" {
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

	if Z2M_DEVMODE {
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
	accName := dev.Definition.Description
	devDesc := strings.TrimSpace(dev.Description)
	if len(devDesc) > 0 {
		accName = devDesc + " " + accName
	}
	acc := accessory.New(accessory.Info{
		Name:         accName,
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

		if Z2M_DEVMODE {
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

	// if no Exposes entry was processed, return error
	if len(allExposes) == 0 {
		return nil, nil, ErrUnknownDeviceType
	}

	initExposeMappings(allExposes...)

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

// Converts a Characteristic value to its corresponding Exposed value
func (m *ExposeMapping) ToExposedValue(v any) (any, error) {
	return m.Translator.ToExposedValue(v)
}

// Calls c.SetValueRequest() with the translated exposed value
// if the error code is -1, there was a translation error.
// Otherwise it's a HAP error code
func (m *ExposeMapping) SetCharacteristicValue(v any) (any, int) {
	cv, err := m.Translator.ToCharacteristicValue(v)
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
	Manufacturer       string `json:"manufacturer,omitempty"`
	ModelId            string `json:"model_id,omitempty"`
	NetworkAddress     int    `json:"network_address"`
	PowerSource        string `json:"power_source,omitempty"`
	SoftwareBuildId    string `json:"software_build_id,omitempty"`
	DateCode           string `json:"date_code,omitempty"`
	Description        string `json:"description,omitempty"`

	Definition *DevDefinition `json:"definition,omitempty"`

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
func (e *ExposesEntry) IsStateSetGet() bool         { return e.Access == 0b111 }
func (e *ExposesEntry) IsStateSettable() bool       { return e.hasAccessBits(0b11) }
func (e *ExposesEntry) IsSettable() bool            { return e.hasAccessBits(0b10) }
func (e *ExposesEntry) hasAccessBits(bits int) bool { return e.Access&bits == bits }

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
