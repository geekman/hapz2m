package hapz2m

import (
	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"

	haplog "github.com/brutella/hap/log"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"crypto/tls"
	"net/url"

	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"reflect"
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

	// Store name for server PIN code
	Z2M_PIN_STORE = "z2m_pin"

	// timeout for UpdateZ2MState
	Z2M_UPDATE_TIMEOUT = 3 * time.Second

	// timeout for marking devices as non-responsive
	Z2M_LAST_SEEN_TIMEOUT = 24 * time.Hour
)

// show more messages for developers
const BRIDGE_DEVMODE = false

type Bridge struct {
	// MQTT broker and credentials
	Server   string
	Username string
	Password string

	// address and interfaces to bind to
	ListenAddr string
	Interfaces []string

	DebugMode bool
	QuietMode bool

	ctx       context.Context
	bridgeAcc *accessory.Bridge

	devices map[string]*BridgeDevice
	server  *hap.Server
	store   hap.Store
	pin     string

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
	LastSeen  time.Time
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

// Sets the PIN code for the HAP server.
// If the given pin is empty, it will be read from the store, or failing that,
// one will be generated
func (br *Bridge) SetPin(pin string) (string, error) {
	// if PIN was not explicitly specified, we re-use the existing one from store
	if pin == "" {
		if storePin, err := br.store.Get(Z2M_PIN_STORE); err == nil {
			pin = string(storePin)
		}
	}

	savePin := pin == ""

	if pin == "" {
		for {
			rnd, err := rand.Int(rand.Reader, big.NewInt(99999999+1))
			if err != nil {
				return "", fmt.Errorf("can't generate PIN: %v", err)
			}

			// pad if necessary
			pin = rnd.Text(10) + "00000000"
			pin = pin[:8]

			// ensure it's not an insecure PIN
			if !hap.InvalidPins[pin] {
				break
			}
		}
	} else if hap.InvalidPins[pin] {
		return "", fmt.Errorf("insecure pin %s", pin)
	}

	// persist the PIN
	if savePin {
		br.store.Set(Z2M_PIN_STORE, []byte(pin))
	}

	br.pin = pin
	return pin, nil
}

// Returns the PIN
func (br *Bridge) GetPin() string { return br.pin }

// Initializes the hap.Server and calls ListenAndServe().
// ListenAndServe() will block until the context is cancelled
func (br *Bridge) StartHAP() error {
	if br.bridgeAcc == nil {
		return fmt.Errorf("bridge accessory not created yet")
	}

	// initialize PIN, either from store or dynamically generated
	if br.pin == "" {
		if _, err := br.SetPin(""); err != nil {
			return err
		}
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

	br.server.Pin = br.pin

	br.server.Addr = br.ListenAddr
	br.server.Ifaces = br.Interfaces

	if br.DebugMode {
		haplog.Debug.Enable()
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

// Return number of devices added to the bridge.
func (br *Bridge) NumDevices() int {
	return len(br.devices)
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
		SetDialer(&net.Dialer{KeepAlive: -1}).
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

	opts.SetConnectionAttemptHandler(func(broker *url.URL, cfg *tls.Config) *tls.Config {
		log.Printf("connecting to MQTT %s...", broker)
		return cfg
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
		if BRIDGE_DEVMODE {
			log.Printf("%s %s\n", k, v)
		}

		// add a last_seen timestamp for saved states without one
		var devState map[string]any
		err = json.Unmarshal(v, &devState)
		if err != nil {
			log.Printf("cannot unmarshal device %s JSON: %v", k, err)
			continue
		}
		if _, hasLastSeen := devState["last_seen"]; !hasLastSeen {
			devState["last_seen"] = time.Now().Add(-Z2M_LAST_SEEN_TIMEOUT)

			// re-marshal the JSON
			newJson, err := json.Marshal(devState)
			if err == nil {
				v = newJson
			} else {
				log.Printf("cannot re-marshal JSON state after adding last_seen for %s: %v", k, err)
			}
		}

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
			// don't bother persisting property if it is "zero"
			if reflect.ValueOf(mapping.Characteristic.Val).IsZero() {
				continue
			}

			v, err := mapping.ToExposedValue(mapping.Characteristic.Val)
			if err != nil {
				return err
			}

			devState[prop] = v
		}

		// serialize into JSON
		lastSeenSince := time.Since(dev.LastSeen)
		if len(devState) > 0 || lastSeenSince < Z2M_LAST_SEEN_TIMEOUT {
			devState["last_seen"] = dev.LastSeen.Unix() * 1000 // timestamp in millis

			jsonState, err := json.Marshal(devState)
			if err != nil {
				return err
			}

			devices[name] = jsonState
		}
	}

	// return early if there was nothing to persist
	if len(devices) == 0 {
		return nil
	}

	allJson, err := json.Marshal(devices)
	if err != nil {
		return err
	}

	return br.store.Set(Z2M_STATE_STORE, allJson)
}

func (br *Bridge) handleMqttMessage(_ mqtt.Client, msg mqtt.Message) {
	topic, payload := msg.Topic(), msg.Payload()

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
		if br.DebugMode {
			log.Printf("received %s: %s", topic, payload)
		}

		br.hapInitMutex.RLock()
		defer br.hapInitMutex.RUnlock()

		isBridgeTopic := strings.HasPrefix(topic, "bridge/")

		// check if HAP bridge device has been initialized
		if !br.hapInitDone {
			if isBridgeTopic {
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
		} else if !isBridgeTopic {
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

func deviceJsonDescriptor(d Device) []byte {
	d.Definition = nil
	j, _ := json.Marshal(d)
	return j
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
			if err == ErrDeviceSkipped || err == ErrUnknownDeviceType {
				continue
			}
			return fmt.Errorf("createAccessory failed: %+v %s", err, deviceJsonDescriptor(dev))

		}

		err = br.AddDevice(&dev, acc, exp)
		if err != nil {
			return fmt.Errorf("AddDevice failed: %+v %s", err, deviceJsonDescriptor(dev))
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

	brdev := &BridgeDevice{dev, acc, em, time.Date(2000, 01, 01, 23, 59, 00, 0, time.Local)}

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

		m.Characteristic.ValueRequestFunc = func(req *http.Request) (any, int) {
			lastSeenSince := time.Since(brdev.LastSeen)

			errCode := 0
			if lastSeenSince >= Z2M_LAST_SEEN_TIMEOUT {
				//log.Printf("dev %s last seen too long ago", name)
				errCode = hap.JsonStatusServiceCommunicationFailure
			}
			return m.Characteristic.Val, errCode
		}
	}

	br.devices[name] = brdev
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

	if br.DebugMode {
		log.Printf("updating Z2M state %q -> %+v", prop, expVal)
	}
	br.PublishState(dev, map[string]any{prop: expVal})

wait:
	for {
		select {
		case updatedVal := <-ch:
			if BRIDGE_DEVMODE {
				log.Printf("received value %q (expected %q) for %s", updatedVal, expVal, key)
			}
			if updatedVal == expVal ||
				// updatedVal is float64 coz that's how Z2M JSON values are, but expVal may not be
				mapping.ExposesEntry.Type == "numeric" && cmpFloat64Numeric(updatedVal, expVal) {

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

// Compare float64 f to numeric value n
// Both parameters are marked as `any`. f will be type-asserted to float64,
// whereas n will be converted to float64 before doing the comparison.
func cmpFloat64Numeric(f, n any) bool {
	if ff, ok := f.(float64); ok {
		nn, ok := valToFloat64(n)
		return ff == nn && ok
	}
	return false
}

// Publish to the MQTT broker for the specific device
func (br *Bridge) PublishState(dev *Device, payload map[string]any) error {
	topic := MQTT_TOPIC_PREFIX + dev.FriendlyName + "/set"
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if br.DebugMode {
		log.Printf("publishing %s: %s", topic, jsonPayload)
	}

	br.mqttClient.Publish(topic, 0, false, jsonPayload)
	return nil
}

// Handle MQTT message to update accessory state
func (br *Bridge) UpdateAccessoryState(devName string, payload []byte) {
	dev := br.devices[devName]
	if dev == nil {
		if br.DebugMode || BRIDGE_DEVMODE {
			log.Printf("unknown device %q", devName)
		}

		// skip unknown device
		return
	}

	if br.DebugMode || (!br.QuietMode && time.Since(dev.LastSeen) > 30*time.Second) {
		log.Printf("received update for device %q", devName)
	}

	var newState map[string]any
	err := json.Unmarshal([]byte(payload), &newState)
	if err != nil {
		log.Printf("unable to parse JSON payload: %v", err)
		return
	}

	lastSeen := time.Now()
	if lastSeenProp, found := newState["last_seen"]; found {
		switch v := lastSeenProp.(type) {
		case float64:
			lastSeen = time.Unix(int64(v/1000), 0)
		case string:
			if lastSeenDate, err := time.Parse(time.RFC3339, v); err == nil {
				lastSeen = lastSeenDate
			} else {
				log.Printf("invalid last_seen timestamp %v", v)
			}
		default:
			log.Printf("invalid last_seen %T %[1]v", v)
		}
	}

	// update LastSeen only if it was valid
	if lastSeen.After(dev.LastSeen) {
		//log.Printf("updating last seen for %s to %s", devName, lastSeen)
		dev.LastSeen = lastSeen
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
				if BRIDGE_DEVMODE {
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
		if BRIDGE_DEVMODE {
			log.Printf("updating %q to %+v", prop, newVal)
		}
		_, errCode := mapping.SetCharacteristicValue(newVal)
		if errCode != 0 {
			log.Printf("unable to update characteristic value for %q: %d", prop, errCode)
		}
	}
}
