package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"sync"
	"time"

	_ "github.com/lib/pq"
	pubnub "github.com/pubnub/go/v6"
)

const VivintSkyEndpoint = "https://www.vivintsky.com/api/"
const PnSubscribeKey = "sub-c-6fb03d68-6a78-11e2-ae8f-12313f022c90"
const PnChannel = "PlatformChannel"

type LoginInfo struct {
	Users struct {
		MessageBroadcastChannel string `json:"mbc"`
		System                  []struct {
			PanelId int64 `json:"panid"`
		} `json:"system"`
	} `json:"u"`
}

type SystemInfo struct {
	System struct {
		Cn         string `json:"cn"`
		Parameters []struct {
			Devices []struct {
				Id   float64  `json:"_id"`
				Type string   `json:"t"`
				Name string   `json:"n"`
				Vd   []string `json:"vd"`
			} `json:"d"`
		} `json:"par"`
	} `json:"system"`
}

func executeLogin(client *http.Client, username string, password string) (LoginInfo, error) {
	log.Println("-- Logging in --")
	jsonString := []byte(fmt.Sprintf(`{"username": "%s", "password": "%s"}`, username, password))

	loginRequest, err := http.NewRequest("POST", VivintSkyEndpoint+"login", bytes.NewBuffer(jsonString))
	loginRequest.Header.Set("Content-Type", "application/json")

	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		return LoginInfo{}, err
	}
	defer loginResponse.Body.Close()

	loginInfo := LoginInfo{}
	json.NewDecoder(loginResponse.Body).Decode(&loginInfo)
	log.Printf("LoginInfo %+v", loginInfo)

	return loginInfo, nil
}

func updateDevices(client *http.Client, db *sql.DB, loginInfo LoginInfo) error {
	log.Printf("-- Updating Devices")
	deviceMap := make(map[float64]string)

	for _, system := range loginInfo.Users.System {
		log.Printf("Getting panel devices: %d", system.PanelId)
		devicesRequest, err := client.Get(VivintSkyEndpoint + "systems/" + strconv.FormatInt(system.PanelId, 10) + "?includerules=false")
		if err != nil {
			return err
		}
		defer devicesRequest.Body.Close()

		systemInfo := SystemInfo{}
		json.NewDecoder(devicesRequest.Body).Decode(&systemInfo)
		log.Printf("System Info: %+v", systemInfo)

		for _, param := range systemInfo.System.Parameters {
			for _, device := range param.Devices {
				deviceMap[device.Id] = device.Name
				// TODO: This should be a batch insert instead of one by one
				sql := `INSERT INTO vivint_device(vivint_id, name, type) VALUES ($1, $2, $3) ON CONFLICT (vivint_id) DO UPDATE SET name=EXCLUDED.name, type=EXCLUDED.type;`
				_, err := db.Exec(sql, device.Id, device.Name, device.Name)
				if err != nil {
					return err
				}
			}
		}
	}

	log.Printf("DeviceMap: %+v", deviceMap)
	return nil
}

func main() {
	logger := log.New(os.Stdout, "govivint: ", log.LstdFlags)
	pubnubLogger := log.New(os.Stderr, "pubnub:   ", log.LstdFlags)

	vivintUsername := os.Getenv("VIVINT_USERNAME")
	vivintPassword := os.Getenv("VIVINT_PASSWORD")
	postgresHost := os.Getenv("POSTGRES_HOST")
	postgresUser := os.Getenv("POSTGRES_USER")
	postgresDb := os.Getenv("POSTGRES_DB")
	postgresPassword := os.Getenv("POSTGRES_PASSWORD")
	deviceUuid := os.Getenv("DEVICE_UUID")

	connStr := fmt.Sprintf("host=%s user=%s dbname=%s password=%s sslmode=disable", postgresHost, postgresUser, postgresDb, postgresPassword)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		logger.Fatal(err)
	}
	defer db.Close()

	var now time.Time
	err = db.QueryRow("SELECT NOW()").Scan(&now)
	if err != nil {
		logger.Fatal(err)
	}

	logger.Printf("Db Now: %s", now)

	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}

	client := &http.Client{
		Jar: jar,
	}

	loginInfo, err := executeLogin(client, vivintUsername, vivintPassword)
	if err != nil {
		logger.Fatal(err)
	}

	err = updateDevices(client, db, loginInfo)
	if err != nil {
		logger.Fatal(err)
	}

	// config := pubnub.NewConfig()
	config := pubnub.NewConfig()
	// SubscribeKey from Admin Portal
	config.SubscribeKey = PnSubscribeKey
	// Reconnection policy selection
	config.PNReconnectionPolicy = pubnub.PNLinearPolicy
	// UUID to be used as a device identifier, a default UUID is generated if not passed
	config.UUID = deviceUuid
	// how long to wait before giving up connection to client
	config.ConnectTimeout = 100
	// how long to keep the subscribe loop running before disconnect
	config.SubscribeRequestTimeout = 310
	// on non subscribe operations, how long to wait for server response
	config.NonSubscribeRequestTimeout = 300
	// heartbeat notifications, by default, the SDK will alert on failed heartbeats.
	// other options such as all heartbeats or no heartbeats are supported.
	config.SetPresenceTimeout(120)
	// The frequency of the pings to the server to state that the client is active
	config.HeartbeatInterval = 60
	// Enable debugging of pubnub by adding a logger
	config.Log = pubnubLogger

	pn := pubnub.NewPubNub(config)

	logger.Printf("Using UUID: %s", config.UUID)

	listener := pubnub.NewListener()
	var wg sync.WaitGroup
	wg.Add(1)

	// In order to keep our devices up to date we will re-login/re-update every 5 minutes... this is probably over zealous because how often do people add new devices to their home...
	go func() {
		time.Sleep(5 * time.Minute)

		loginInfo, err := executeLogin(client, vivintUsername, vivintPassword)
		if err != nil {
			logger.Fatal(err)
		}

		err = updateDevices(client, db, loginInfo)
		if err != nil {
			logger.Fatal(err)
		}
	}()

	go func() {
		for {
			logger.Println("-- Listening for a message")
			select {
			case status := <-listener.Status:
				logger.Printf("Status: %+v", status)
				switch status.Category {
				case pubnub.PNDisconnectedCategory:
					// This event happens when radio / connectivity is lost
					logger.Println("PNDisconnectedCategory")
				case pubnub.PNConnectedCategory:
					// Connect event. You can do stuff like publish, and know you'll get it.
					// Or just use the connected event to confirm you are subscribed for
					// UI / internal notifications, etc
					logger.Println("PNConnectedCategory")
				case pubnub.PNReconnectedCategory:
					// Happens as part of our regular operation. This event happens when
					// radio / connectivity is lost, then regained.
					logger.Println("PNReconnectedCategory")
				}
			case message := <-listener.Message:
				logger.Printf("Message: %+v", message)

				// Handle new message stored in message.message
				if message.Channel != "" {
					logger.Println("message.Channel", message.Channel)
					// Message has been received on channel group stored in
					// message.Channel
				} else {
					logger.Println("message.Subscription", message.Subscription)
					// Message has been received on channel stored in
					// message.Subscription
				}

				messageString, err := json.Marshal(message.Message)
				if err != nil {
					logger.Fatal(err)
				}

				insertedDeviceRecord := false
				if msg, ok := message.Message.(map[string]interface{}); ok {
					if da, ok := msg["da"].(map[string]interface{}); ok {
						if devices, ok := da["d"].([]interface{}); ok {
							logger.Println("---------------================== Start Device Message ==================---------------")
							insertedDeviceRecord = true
							logger.Printf("Devices: %+v", devices)
							logger.Println("messageString", string(messageString))

							devicesString, err := json.Marshal(devices)
							if err != nil {
								logger.Fatal(err)
							}

							logger.Println("devicesString", string(devicesString))

							sql := `INSERT INTO vivint_event(devices, data) VALUES ($1, $2);`
							_, err = db.Exec(sql, string(devicesString), string(messageString))
							if err != nil {
								logger.Fatal(err)
							}

							logger.Println("---------------================== End Device Message ==================---------------")
						}
					}
				}

				if !insertedDeviceRecord {
					logger.Println("---------------================== Start Message ==================---------------")
					logger.Println("messageString", string(messageString))
					sql := `INSERT INTO vivint_event(data) VALUES ($1);`
					_, err = db.Exec(sql, string(messageString))
					if err != nil {
						logger.Fatal(err)
					}
					logger.Println("---------------================== End Message ==================---------------")
				}

				// donePublish <- true
			case presence := <-listener.Presence:
				logger.Printf("Presence: %+v", presence)

			case signal := <-listener.Signal:
				logger.Printf("Signal: %+v", signal)

			case uuidEvent := <-listener.UUIDEvent:
				logger.Printf("UUIDEvent: %+v", uuidEvent)

			case channelEvent := <-listener.ChannelEvent:
				logger.Printf("ChannelEvent: %+v", channelEvent)

			case membershipEvent := <-listener.MembershipEvent:
				logger.Printf("MembershipEvent: %+v", membershipEvent)

			case messageActionsEvent := <-listener.MessageActionsEvent:
				logger.Printf("MessageActionsEvent: %+v", messageActionsEvent)

			case file := <-listener.File:
				logger.Printf("File: %+v", file)
			}
		}
	}()

	pn.AddListener(listener)

	pnChannel := PnChannel + "#" + loginInfo.Users.MessageBroadcastChannel
	logger.Println("pnChannel", pnChannel)
	pn.Subscribe().Channels([]string{pnChannel}).Execute()

	// TODO: This will just wait forever since we never call wg.Done() anywhere ... we should wait on something useful
	wg.Wait()
}
