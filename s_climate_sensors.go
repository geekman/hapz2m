package hapz2m

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/service"
)

func createClimateServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		// climate sensors are numeric only
		if exp.Type != "numeric" {
			continue
		}

		exp := exp // create a copy

		switch {
		case exp.Name == "temperature":
			s := service.NewTemperatureSensor()

			// doesn't do anything, since the Home app seem to round values to 0.25 increments regardless
			// https://developer.apple.com/forums/thread/674461
			// https://github.com/home-assistant/core/issues/45332
			s.CurrentTemperature.SetStepValue(0.01)

			svcs = append(svcs, s.S)
			exposes = append(exposes, NewExposeMapping(&exp, s.CurrentTemperature.C))

		case exp.Name == "humidity":
			s := service.NewHumiditySensor()
			s.CurrentRelativeHumidity.SetStepValue(0.01) // ditto, but 1% increments
			svcs = append(svcs, s.S)
			exposes = append(exposes, NewExposeMapping(&exp, s.CurrentRelativeHumidity.C))

		case exp.Name == "pressure":
			// TODO looks like pressure is not standard in HomeKit
			// Eve has custom UUIDs: https://gist.github.com/simont77/3f4d4330fa55b83f8ca96388d9004e7d
		}
	}

	return accessory.TypeSensor, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createClimateServices)
}
