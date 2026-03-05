package hapz2m

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"slices"
)

func createStatelesswitchServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		switch {
		case exp.Type == "enum" && exp.Name == "action" && slices.Contains(exp.Values, "single"):
			sw := service.NewStatelessProgrammableSwitch()

			svcs = append(svcs, sw.S)

			t := &EnumTranslator{map[string]any{
				"single": characteristic.ProgrammableSwitchEventSinglePress,
				"double": characteristic.ProgrammableSwitchEventDoublePress,
				"hold":   characteristic.ProgrammableSwitchEventLongPress,

				// unsupported actions, map to nil to ignore
				"triple":    nil,
				"quadruple": nil,
				"release":   nil,
			}}
			m := NewTranslatedExposeMapping(&exp, sw.ProgrammableSwitchEvent.C, t)
			m.IsTransient = true
			exposes = append(exposes, m)
		}
	}

	// TODO implement some kind of priority or tie-breaking
	return accessory.TypeProgrammableSwitch, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createStatelesswitchServices)
}
