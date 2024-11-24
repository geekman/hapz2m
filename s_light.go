package hapz2m

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

func createLightServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		switch {
		case exp.Type == "light" && len(exp.Features) > 0:
			exp := exp // create a copy

			light := service.NewLightbulb()

			for _, feat := range exp.Features {
				if !feat.IsStateSettable() {
					continue
				}

				feat := feat

				switch {
				case feat.Name == "state" && feat.Type == "binary":
					svcs = append(svcs, light.S)
					exposes = append(exposes, NewExposeMapping(&feat, light.On.C))

				case feat.Name == "brightness" && feat.Type == "numeric":
					brightness := characteristic.NewBrightness()
					light.AddC(brightness.C)
					exposes = append(exposes, NewExposeMapping(&feat, brightness.C))
				}
			}
		}
	}

	return accessory.TypeLightbulb, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createLightServices)
}
