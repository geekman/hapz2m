package hapz2m

import (
	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrDeviceExists            = fmt.Errorf("device already exists")
	ErrDuplicateExposesMapping = fmt.Errorf("duplicate property name in Exposes mapping")
	ErrUpdateTimeout           = fmt.Errorf("update timeout")
	ErrAlreadyConnected        = fmt.Errorf("already connected")
)

const (
	MQTT_TOPIC_PREFIX = "zigbee2mqtt/"

	// Store name for persisting zigbee2mqtt state
	Z2M_STATE_STORE = "z2m_state"

	// timeout for UpdateZ2MState
	Z2M_UPDATE_TIMEOUT = 3 * time.Second
)

const BRIDGE_DEBUG = false

type Bridge struct {
	// MQTT broker and credentials
	Server   string
	Username string
	Password string

	ctx       context.Context
	bridgeAcc *accessory.Bridge

	devices map[string]*BridgeDevice
	server  *hap.Server
	store   hap.Store

	// RWMutex protects hap init variables below
	hapInitMutex   sync.RWMutex
	hapInitDone    bool
	hapInitCh      chan struct{}
	pendingUpdates sync.Map // queued updates before HAP init

	mqttClient mqtt.Client

	updateListeners sync.Map
}

type BridgeDevice struct {
	Device    *Device
	Accessory *accessory.A
	Mappings  map[string]*ExposeMapping
}

// Creates and initializes a Bridge.
func NewBridge(ctx context.Context, storeDir string) *Bridge {
	br := &Bridge{
		ctx:   ctx,
		store: hap.NewFsStore(storeDir),

		hapInitCh: make(chan struct{}),
		devices:   make(map[string]*BridgeDevice),
	}

	br.bridgeAcc = accessory.NewBridge(accessory.Info{
		Name:         "hap-z2m Bridge",
		Manufacturer: "geekman",
	})

	return br
}

// Initializes the hap.Server and calls ListenAndServe().
// ListenAndServe() will block until the context is cancelled
func (br *Bridge) StartHAP() error {
	if br.bridgeAcc == nil {
		return fmt.Errorf("bridge accessory not created yet")
	}

	br.hapInitMutex.RLock()
	if !br.hapInitDone {
		br.hapInitMutex.RUnlock()
		return fmt.Errorf("HAP accessories not yet initialized")
	}

	acc := br.accessories()
	br.hapInitMutex.RUnlock()

	var err error
	br.server, err = hap.NewServer(br.store, br.bridgeAcc.A, acc...)
	if err != nil {
		return err
	}

	err = br.server.ListenAndServe(br.ctx)

	// disconnect from MQTT
	br.mqttClient.Disconnect(1000)

	// flush z2m state to disk
	if err := br.saveZ2MState(); err != nil {
		log.Printf("cannot persist Z2M state: %s", err)
	}

	return err
}

// Waits until the Bridge has configured itself from Z2M state.
// Once that happens, subsequent calls return immediately.
func (br *Bridge) WaitConfigured() {
	for !br.hapInitDone {
		<-br.hapInitCh
	}
}

// Connects to the MQTT server.
// Blocks until the connection is established, then auto-reconnect logic takes over
func (br *Bridge) ConnectMQTT() error {
	if br.mqttClient != nil && br.mqttClient.IsConnected() {
		return ErrAlreadyConnected
	}

	opts := mqtt.NewClientOptions().
		AddBroker(br.Server).
		SetUsername(br.Username).
		SetPassword(br.Password).
		SetClientID("hap-z2m").
		SetKeepAlive(60 * time.Second).
		SetPingTimeout(2 * time.Second).
		SetConnectRetry(true)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Printf("connected to MQTT broker")

		tok := c.Subscribe(MQTT_TOPIC_PREFIX+"#", 0, br.handleMqttMessage)
		if tok.Wait() && tok.Error() != nil {
			log.Fatal(tok.Error())
		}

		log.Printf("subscribed to MQTT topic")
	})

	br.mqttClient = mqtt.NewClient(opts)

	if tok := br.mqttClient.Connect(); tok.Wait() && tok.Error() != nil {
		return tok.Error()
	}

	return nil
}

// Load the Z2M state from hap.Store into the sync.Map.
// If the state was blank or not found, a nil error will be returned.
// Existing state for devices in the Map will not be overwritten.
func (br *Bridge) loadZ2MState(m *sync.Map) error {
	state, err := br.store.Get(Z2M_STATE_STORE)
	if err != nil || len(state) == 0 {
		return nil
	}

	var stateMap map[string][]byte
	err = json.Unmarshal(state, &stateMap)
	if err != nil {
		return err
	}

	for k, v := range stateMap {
		// LoadOrStore retains existing data, only storing if empty
		if _, exists := m.LoadOrStore(k, v); exists {
			log.Printf("skipping %s, newer data is available", k)
		}
	}

	return nil
}

// Persists the Z2M state into hap.Store
// Returns an error if there was a problem with translation, serialization or storing.
func (br *Bridge) saveZ2MState() error {
	devices := make(map[string][]byte)

	for name, dev := range br.devices {
		devState := make(map[string]any)

		for prop, mapping := range dev.Mappings {
			v, err := mapping.ToExposedValue(mapping.Characteristic.Val)
			if err != nil {
				return err
			}

			devState[prop] = v
		}

		// serialize into JSON
		jsonState, err := json.Marshal(devState)
		if err != nil {
			return err
		}

		devices[name] = jsonState
	}

	allJson, err := json.Marshal(devices)
	if err != nil {
		return err
	}

	return br.store.Set(Z2M_STATE_STORE, allJson)
}

func (br *Bridge) handleMqttMessage(_ mqtt.Client, msg mqtt.Message) {
	topic, payload := msg.Topic(), msg.Payload()

	//log.Printf("received %s %s", topic, payload)

	// check for topic prefix and remove it
	l := len(MQTT_TOPIC_PREFIX)
	if len(topic) <= l || topic[:l] != MQTT_TOPIC_PREFIX {
		return
	}
	topic = topic[l:]

	// strip leading slashes if we have to
	if topic[0] == '/' {
		topic = topic[1:]
	}

	// ignore /set and /get requests, not sent by z2m
	l = len(topic)
	if l > len("/get") {
		topicSuffix := topic[l-4:]
		if topicSuffix == "/get" || topicSuffix == "/set" {
			return
		}
	}

	// spawn a goroutine to handle message, since mutex might block
	go func() {
		br.hapInitMutex.RLock()
		defer br.hapInitMutex.RUnlock()

		// check if HAP bridge device has been initialized
		if !br.hapInitDone {
			if strings.HasPrefix(topic, "bridge/") {
				// need to look out for bridge/devices for intiial setup
				if topic == "bridge/devices" {
					err := br.AddDevicesFromJSON(payload)
					if err != nil {
						log.Printf("unable to add devices from JSON: %v", err)
					}

					// populate initial characteristic values from cache, if available
					err = br.loadZ2MState(&br.pendingUpdates)
					if err != nil {
						log.Printf("cannot load z2m state: %s", err)
					}

					// "upgrade" to write lock for modifications
					br.hapInitMutex.RUnlock()     // unlock now, relock again later
					defer br.hapInitMutex.RLock() // ... before defer kicks in

					br.hapInitMutex.Lock()
					defer br.hapInitMutex.Unlock()

					// dequeue and apply state updates
					log.Print("applying deferred updates...")
					br.pendingUpdates.Range(func(k, v any) bool {
						devName := k.(string)
						br.UpdateAccessoryState(devName, v.([]byte))
						return true // continue
					})

					br.hapInitDone = true

					close(br.hapInitCh) // signal to waiting threads
				}
			} else {
				// queue other messages for state updating after setup
				// only keep latest message for each device
				log.Printf("queueing updates for %s", topic)
				br.pendingUpdates.Store(topic, payload)
			}
		} else {
			br.UpdateAccessoryState(topic, payload)
		}
	}()
}

// Gets a list of all added accessories
func (br *Bridge) accessories() []*accessory.A {
	var acc []*accessory.A
	for _, d := range br.devices {
		acc = append(acc, d.Accessory)
	}
	return acc
}

// Creates and calls AddDevice() based on the JSON definitions from zigbee2mqtt/bridge/devices.
func (br *Bridge) AddDevicesFromJSON(devJson []byte) error {
	var devices []Device
	err := json.Unmarshal(devJson, &devices)
	if err != nil {
		return err
	}

	for _, dev := range devices {
		dev := dev // make a copy

		acc, exp, err := createAccessory(&dev)
		if err != nil {
			if err == ErrDeviceSkipped {
				continue
			}
			return err
		}

		err = br.AddDevice(&dev, acc, exp)
		if err != nil {
			return err
		}
	}

	return nil
}

// Adds a device to this Bridge
func (br *Bridge) AddDevice(dev *Device, acc *accessory.A, mappings []*ExposeMapping) error {
	name := dev.FriendlyName
	if _, exists := br.devices[name]; exists {
		return ErrDeviceExists
	}

	// put ExposeMapping into a map
	em := make(map[string]*ExposeMapping)
	for _, m := range mappings {
		prop := m.ExposesEntry.Property
		if _, exists := em[prop]; exists {
			return ErrDuplicateExposesMapping
		}
		em[prop] = m
	}

	// wire up accessory's remote value update functions
	for _, m := range mappings {
		m := m
		if m.ExposesEntry.IsSettable() {
			m.Characteristic.SetValueRequestFunc = func(newVal any, req *http.Request) (any, int) {
				// handle remote value updates only
				if req != nil {
					updated, err := br.UpdateZ2MState(dev, m, newVal)
					if !updated {
						log.Printf("error updating z2m for %s: %s", dev.FriendlyName, err)
						return nil, hap.JsonStatusServiceCommunicationFailure
					}
				}
				return nil, 0
			}
		}
	}

	br.devices[name] = &BridgeDevice{dev, acc, em}
	return nil
}

func makeUpdateKey(dev *Device, mapping *ExposeMapping) string {
	return dev.FriendlyName + "/" + mapping.ExposesEntry.Property
}

// Updates zigbee2mqtt device state over MQTT.
// It then waits (with timeout) for zigbee2mqtt to send the updated state via MQTT,
// as acknowledgement of receipt, before returning with an updated status.
// If z2m doesn't respond in time, ErrUpdateTimeout is returned.
func (br *Bridge) UpdateZ2MState(dev *Device, mapping *ExposeMapping, newVal any) (updated bool, err error) {
	prop := mapping.ExposesEntry.Property

	// map Characteristic to exposed value
	expVal, err := mapping.ToExposedValue(newVal)
	if err != nil {
		return updated, err
	}

	ch := make(chan any, 2)
	defer close(ch)

	// only one update should occur at a time
	key := makeUpdateKey(dev, mapping)
	if _, exists := br.updateListeners.LoadOrStore(key, ch); exists {
		return updated, fmt.Errorf("already a pending update on property %s", key)
	}

	ctx, cancel := context.WithTimeout(br.ctx, Z2M_UPDATE_TIMEOUT)
	defer cancel()

	log.Printf("updating Z2M state %q -> %+v", prop, expVal)
	br.PublishState(dev, map[string]any{prop: expVal})

wait:
	for {
		select {
		case updatedVal := <-ch:
			if BRIDGE_DEBUG {
				log.Printf("received value %q (expected %q) for %s", updatedVal, expVal, key)
			}
			if updatedVal == expVal {
				updated = true
				break wait
			}

		case <-ctx.Done():
			break wait
		}
	}

	//br.updateListeners.CompareAndDelete(key, ch)	// needs go 1.20
	br.updateListeners.Delete(key)

	if !updated {
		err = ErrUpdateTimeout
	}
	return updated, err
}

// Publish to the MQTT broker for the specific device
func (br *Bridge) PublishState(dev *Device, payload map[string]any) error {
	topic := MQTT_TOPIC_PREFIX + dev.FriendlyName + "/set"
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if BRIDGE_DEBUG {
		log.Printf("publishing %q payload %s", topic, jsonPayload)
	}

	br.mqttClient.Publish(topic, 0, false, jsonPayload)
	return nil
}

// Handle MQTT message to update accessory state
func (br *Bridge) UpdateAccessoryState(devName string, payload []byte) {
	dev := br.devices[devName]
	if dev == nil {
		// skip unknown device
		//log.Printf("unknown device %q", devName)
		return
	}

	if BRIDGE_DEBUG {
		log.Printf("received update for device %q", devName)
	}

	var newState map[string]any
	err := json.Unmarshal([]byte(payload), &newState)
	if err != nil {
		log.Printf("unable to parse JSON payload: %v", err)
		return
	}

	for prop, mapping := range dev.Mappings {
		newVal, exists := newState[prop]
		if !exists {
			continue
		}

		// send updates via channel if requested
		if mapping.ExposesEntry.IsSettable() {
			updateKey := makeUpdateKey(dev.Device, mapping)
			if ch, waiting := br.updateListeners.Load(updateKey); waiting {
				if BRIDGE_DEBUG {
					log.Printf("sending new value for %q via chan", updateKey)
				}
				select {
				case ch.(chan any) <- newVal:
				default:
					// couldn't send message
					log.Printf("cannot deliver updated value via channel")
				}

				continue // next property
			}
		}

		// update value into Characteristic
		if BRIDGE_DEBUG {
			log.Printf("updating %q to %+v", prop, newVal)
		}
		_, errCode := mapping.SetCharacteristicValue(newVal)
		if errCode != 0 {
			log.Printf("unable to update characteristic value for %q: %d", prop, errCode)
		}
	}
}