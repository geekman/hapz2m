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

func TestBridgePersistState(t *testing.T) {
	dir, err := os.MkdirTemp("", "hapz2m-bridge*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	var m sync.Map
	ctx := context.Background()

	b := NewBridge(ctx, dir)

	// devices
	s1 := fmt.Appendf(nil, ContactSensorTemplate, 10)
	s2 := fmt.Appendf(nil, ContactSensorTemplate, 20)

	err = b.AddDevicesFromJSON(fmt.Appendf(nil, "[%s, %s]", s1, s2))
	if err != nil {
		t.Fatalf("cannot add devices: %v", err)
	}

	// empty state, should have no errors
	t.Logf("loading from empty db")
	if err := b.loadZ2MState(&m); err != nil {
		t.Errorf("empty state load should not error: %v", err)
	}

	// save 2 devices
	t.Logf("persisting state")
	if err := b.saveZ2MState(); err != nil {
		t.Errorf("can't persist state: %v", err)
	}

	// re-create with less devices
	b2 := NewBridge(ctx, dir)
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
