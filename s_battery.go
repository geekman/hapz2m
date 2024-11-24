package hapz2m

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

func createBatteryServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		switch {
		case exp.Type == "numeric" && exp.Name == "battery":
			exp := exp // create a copy
			batt := service.NewBatteryService()
			batt.ChargingState.SetValue(characteristic.ChargingStateNotChargeable)

			svcs = append(svcs, batt.S)
			exposes = append(exposes, NewExposeMapping(&exp, batt.BatteryLevel.C))
		}
	}

	return accessory.TypeUnknown, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createBatteryServices)
}
