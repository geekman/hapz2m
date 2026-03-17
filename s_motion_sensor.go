package hapz2m

import (
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/service"
)

// there isn't a distinction between motion and occupancy sensors in z2m,
// but most sensors are PIR, so they are technically motion sensors

func createMotionServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		switch {
		case exp.Type == "binary" && exp.Name == "occupancy":
			exp := exp // create a copy

			s := service.NewMotionSensor()

			m := NewExposeMapping(&exp, s.MotionDetected.C)
			m.SetupDebouncedValue(false, 1*time.Minute) // debounce "no motion" value

			svcs = append(svcs, s.S)
			exposes = append(exposes, m)
		}
	}

	return accessory.TypeSensor, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createMotionServices)
}
