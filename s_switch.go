package hapz2m

import (
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"

	"regexp"
)

var outletRegex = regexp.MustCompile(`(?i)\boutlet\b`)

func createSwitchServices(dev *Device) (byte, []*service.S, []*ExposeMapping, error) {
	var svcs []*service.S
	var exposes []*ExposeMapping

	for _, exp := range dev.Definition.Exposes {
		if exp.Ignored() {
			continue
		}

		switch {
		case exp.Type == "switch" && len(exp.Features) > 0:
			exp := exp // create a copy

			f := exp.Features[0]
			if f.Name == "state" && f.Type == "binary" && f.IsStateSetGet() {
				sw := service.NewSwitch()

				// add a name/label for this switch
				n := characteristic.NewName()
				n.SetValue(f.Endpoint)
				sw.AddC(n.C)

				svcs = append(svcs, sw.S)
				exposes = append(exposes, &ExposeMapping{&f, sw.On.C, nil})
			}
		}
	}

	accTyp := accessory.TypeSwitch
	if outletRegex.MatchString(dev.Definition.Description) {
		accTyp = accessory.TypeOutlet
	}

	return accTyp, svcs, exposes, nil
}

func init() {
	RegisterCreateServiceHandler(createSwitchServices)
}
