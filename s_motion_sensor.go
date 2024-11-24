package hapz2m

import (
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/service"
)

// there isn't a distinction between motion and occupancy sensors in z2m,
// but most sensors are PIR, so they are technically motion sensors

func setupDelayedOff(m *ExposeMapping, delay time.Duration) {
	// lock makes sure Timer and setFunc aren't stepping over each other
	lock := &sync.Mutex{}

	tm := time.AfterFunc(1*time.Minute, func() {
		lock.Lock()
		defer lock.Unlock()

		m.Characteristic.SetValueRequest( /*motion*/ false, nil)
	})
	tm.Stop()

	m.SetCharacteristicValueFunc = func(m *ExposeMapping, newVal any, changed bool) (bool, error) {
		lock.Lock()
		defer lock.Unlock()

		if newVal == true { // motion detected
			tm.Stop()
		} else { // hold off transitions to no motion
			if changed {
				tm.Reset(delay)
			}
			return false, nil
		}

		return true, nil
	}
}

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
			setupDelayedOff(m, 1*time.Minute)

			svcs = append(svcs, s.S)
			exposes = append(exposes, m)
		}
	}

	return accessory.TypeSensor, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createMotionServices)
}
