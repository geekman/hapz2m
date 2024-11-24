package hapz2m

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

func createContactServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		switch {
		case exp.Type == "binary" && exp.Name == "contact":
			exp := exp // create a copy

			s := service.NewContactSensor()

			svcs = append(svcs, s.S)
			exposes = append(exposes, NewTranslatedExposeMapping(&exp, s.ContactSensorState.C,
				&BoolTranslator{
					characteristic.ContactSensorStateContactNotDetected,
					characteristic.ContactSensorStateContactDetected}))
		}
	}

	return accessory.TypeSensor, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createContactServices)
}
