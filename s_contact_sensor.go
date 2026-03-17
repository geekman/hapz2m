package hapz2m

import (
	"time"

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

			m := NewTranslatedExposeMapping(&exp, s.ContactSensorState.C,
				&BoolTranslator{
					characteristic.ContactSensorStateContactNotDetected,
					characteristic.ContactSensorStateContactDetected})

			// hold off "closed" state for at least 5s, in case user forgot something and re-opens the door
			m.SetupDebouncedValue(characteristic.ContactSensorStateContactDetected, 5*time.Second)

			svcs = append(svcs, s.S)
			exposes = append(exposes, m)
		}
	}

	return accessory.TypeSensor, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createContactServices)
}
