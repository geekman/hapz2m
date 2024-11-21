package hapz2m

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
)

const ContactSensorTemplate = `
		{
        "network_address": %[1]d,
        "friendly_name": "0x00158d00aabbcc%02[1]d",
        "ieee_address":  "0x00158d00aabbcc%02[1]d",
        "interview_completed": true,
        "interviewing": false,
		"disabled": false,
        "supported": true,
		"type": "EndDevice",
		"definition": {
			"exposes": [
                {
                    "access": 1,
                    "name": "contact",
                    "property": "contact",
                    "type": "binary",
                    "value_off": "A",
                    "value_on": "B"
                }
				]
			}
		}`

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	sz := int64(0)
	if err == nil {
		sz = fi.Size()
	}
	return sz, err
}

func TestBridgePersistState(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge(context.Background(), dir)

	// devices
	s1 := fmt.Appendf(nil, ContactSensorTemplate, 10)
	s2 := fmt.Appendf(nil, ContactSensorTemplate, 20)

	err := b.AddDevicesFromJSON(fmt.Appendf(nil, "[%s, %s]", s1, s2))
	if err != nil {
		t.Fatalf("cannot add devices: %v", err)
	}
	if len(b.devices) != 2 {
		t.Fatalf("devices not added to bridge!")
	}

	// empty state, should have no errors
	t.Logf("loading from empty db")
	var m sync.Map
	if err := b.loadZ2MState(&m); err != nil {
		t.Errorf("empty state load should not error: %v", err)
	}

	// save 2 devices
	t.Logf("persisting state")
	if err := b.saveZ2MState(); err != nil {
		t.Errorf("can't persist state: %v", err)
	}

	storeFname := dir + string(os.PathSeparator) + Z2M_STATE_STORE

	if sz, err := fileSize(storeFname); err == nil && sz != 0 {
		t.Errorf("expecting defaults to not be persisted, but got size %d, err %v", sz, err)
	}

	// alter sensor states to non-defaults
	for _, dev := range b.devices {
		dev.Mappings["contact"].Characteristic.Val = 1
	}

	// save 2 devices, again
	t.Logf("persisting state with non-defaults")
	if err := b.saveZ2MState(); err != nil {
		t.Errorf("can't persist state: %v", err)
	}

	if sz, err := fileSize(storeFname); err != nil || sz == 0 {
		t.Errorf("expecting state to be persisted, but got size %d, err %v", sz, err)
	}

	// re-create with less devices
	b2 := NewBridge(context.Background(), t.TempDir())
	b2.AddDevicesFromJSON(fmt.Appendf(nil, "[%s]", s1))

	t.Logf("loading from initial db")
	if err := b2.loadZ2MState(&m); err != nil {
		t.Errorf("load err: %v", err)
	}

	t.Logf("persisting state")
	if err := b2.saveZ2MState(); err != nil {
		t.Errorf("can't persist state: %v", err)
	}

	// reload from smaller-sized state
	t.Logf("re-loading from db")
	if err := b2.loadZ2MState(&m); err != nil {
		t.Errorf("load err: %v", err)
	}

}

func TestBridgeAddCoordinator(t *testing.T) {
	b := NewBridge(context.Background(), t.TempDir())

	err := b.AddDevicesFromJSON([]byte(`
		[{
		"definition": null,
		"disabled": false,
		"endpoints": [{"foo": true}],
		"friendly_name": "Coordinator",
		"ieee_address": "0x0022222200000000",
		"interview_completed": true,
		"interviewing": false,
		"network_address": 0,
		"supported": true,
		"type": "Coordinator"
		}]
	`))
	if err != nil {
		t.Fatalf("cannot add coordinator: %v", err)
	}
}
