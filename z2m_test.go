package hapz2m

import (
	"encoding/json"
	"testing"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

const ContactSensor = `{
        "date_code": "20161128",
        "definition": {
            "description": "Aqara door & window contact sensor",
            "exposes": [
                {
                    "access": 1,
                    "description": "Remaining battery in %, can take up to 24 hours before reported.",
                    "name": "battery",
                    "property": "battery",
                    "type": "numeric",
                    "unit": "%",
                    "value_max": 100,
                    "value_min": 0
                },
                {
                    "access": 1,
                    "description": "Indicates if the contact is closed (= true) or open (= false)",
                    "name": "contact",
                    "property": "contact",
                    "type": "binary",
                    "value_off": true,
                    "value_on": false
                },
                {
                    "access": 1,
                    "description": "Temperature of the device",
                    "name": "device_temperature",
                    "property": "device_temperature",
                    "type": "numeric",
                    "unit": "°C"
                },
                {
                    "access": 1,
                    "description": "Voltage of the battery in millivolts",
                    "name": "voltage",
                    "property": "voltage",
                    "type": "numeric",
                    "unit": "mV"
                },
                {
                    "access": 1,
                    "description": "Number of power outages",
                    "name": "power_outage_count",
                    "property": "power_outage_count",
                    "type": "numeric"
                },
                {
                    "access": 1,
                    "description": "Link quality (signal strength)",
                    "name": "linkquality",
                    "property": "linkquality",
                    "type": "numeric",
                    "unit": "lqi",
                    "value_max": 255,
                    "value_min": 0
                }
            ],
            "model": "MCCGQ11LM",
            "options": [
                {
                    "access": 2,
                    "description": "Calibrates the device_temperature value (absolute offset), takes into effect on next report of device.",
                    "name": "device_temperature_calibration",
                    "property": "device_temperature_calibration",
                    "type": "numeric"
                }
            ],
            "supports_ota": false,
            "vendor": "Xiaomi"
        },
        "disabled": false,
        "endpoints": {
            "1": {
                "bindings": [],
                "clusters": {
                    "input": [
                        "genBasic",
                        "genIdentify",
                        "65535",
                        "genOnOff"
                    ],
                    "output": [
                        "genBasic",
                        "genGroups",
                        "65535"
                    ]
                },
                "configured_reportings": [],
                "scenes": []
            }
        },
        "friendly_name": "0x00158d00aabbccdd",
        "ieee_address":  "0x00158d00aabbccdd",
        "interview_completed": true,
        "interviewing": false,
        "manufacturer": "LUMI",
        "model_id": "lumi.sensor_magnet.aq2",
        "network_address": 18350,
        "power_source": "Battery",
        "software_build_id": "3000-0001",
        "supported": true,
        "type": "EndDevice"
    }`

func TestCreateAccessory(t *testing.T) {
	var dev Device
	err := json.Unmarshal([]byte(ContactSensor), &dev)
	if err != nil {
		panic(err)
	}

	acc, exp, err := createAccessory(&dev)
	if err != nil {
		t.Fatal(err)
	}
	dumpAccessory(acc)
	_ = exp
}

func exposeMappingFromJson(jstr string) *ExposesEntry {
	exp := &ExposesEntry{}
	err := json.Unmarshal([]byte(jstr), &exp)
	if err != nil {
		panic(err)
	}
	return exp
}

func TestMappingTranslation(t *testing.T) {
	exp := exposeMappingFromJson(`{
		"access": 1,
		"name": "contact",
		"property": "contact",
		"type": "binary",
		"value_off": "NO_CONTACT",
		"value_on": "CONTACT"
	}`)

	s := service.NewContactSensor()
	m := &ExposeMapping{exp, s.ContactSensorState.C,
		&BoolTranslator{
			characteristic.ContactSensorStateContactDetected,
			characteristic.ContactSensorStateContactNotDetected}}

	for _, test := range []struct{ e, c any }{
		{"CONTACT", characteristic.ContactSensorStateContactDetected},
		{"NO_CONTACT", characteristic.ContactSensorStateContactNotDetected},
	} {
		v, errCode := m.SetCharacteristicValue(test.e)
		t.Logf("exposed = %+v, v = %+v", test.e, v)
		if errCode != 0 {
			t.Fatalf("cant set cvalue, err %d", errCode)
		}

		newV := m.Characteristic.Val
		if newV != test.c {
			t.Fatalf("characteristic value was wrong. wanted %v got %v", test.c, v)
		}
	}
	if _, errCode := m.SetCharacteristicValue("NO_CONTACTSS"); errCode != -1 {
		t.Fatalf("expected translation error, but errCode was %d", errCode)
	}
}

func TestMappingNumeric(t *testing.T) {
	exp := exposeMappingFromJson(`{
		"access": 1,
		"name": "Temperature",
		"property": "temperature",
		"type": "numeric",
		"unit": "°C"
	}`)

	s := service.NewTemperatureSensor()
	m := &ExposeMapping{exp, s.CurrentTemperature.C, nil}

	for _, test := range []struct {
		v    any
		desc string
	}{
		{10.0, "temp 10.0"},
		{10.5, "temp 10.5"},
		{0.5, "temp 0.5"},
		{0., "temp 0"},
	} {
		_, errCode := m.SetCharacteristicValue(test.v)
		if errCode != 0 {
			t.Fatalf("errCode for %s: %d", test.desc, errCode)
		}
		cv := s.CurrentTemperature.Value()
		t.Logf("%s: ctemp %f", test.desc, cv)

		if test.v != cv {
			t.Fatalf("%s mismatch. want %f, got %f", test.desc, test.v, cv)
		}

		ev, err := m.ToExposedValue(cv)
		if err != nil {
			t.Fatalf("err converting back %s: %v", test.desc, err)
		}

		if ev != test.v {
			t.Fatalf("%s reverse: want %f, got %f", test.desc, test.v, ev)
		}
	}
}
