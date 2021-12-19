package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
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

func executeLogin(logger *log.Logger, client *http.Client, username string, password string) (LoginInfo, error) {
	logger.Println("-- Logging in --")
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
	logger.Printf("LoginInfo %+v", loginInfo)

	return loginInfo, nil
}

func updateDevices(logger *log.Logger, client *http.Client, db *sql.DB, loginInfo LoginInfo) error {
	logger.Printf("-- Updating Devices")
	deviceMap := make(map[float64]string)

	if len(loginInfo.Users.System) > 0 {
		for _, system := range loginInfo.Users.System {
			logger.Printf("Getting panel devices: %d", system.PanelId)
			devicesRequest, err := client.Get(VivintSkyEndpoint + "systems/" + strconv.FormatInt(system.PanelId, 10) + "?includerules=false")
			logger.Printf("DevicesRequest: %+v err: %s", devicesRequest, err)
			if err != nil {
				return err
			}
			defer devicesRequest.Body.Close()

			systemInfo := SystemInfo{}
			json.NewDecoder(devicesRequest.Body).Decode(&systemInfo)
			logger.Printf("System Info: %+v", systemInfo)

			for _, param := range systemInfo.System.Parameters {
				for _, device := range param.Devices {
					deviceMap[device.Id] = device.Name
					// TODO: This should be a batch insert instead of one by one
					insertSql := `INSERT INTO vivint_device(vivint_id, name, type) VALUES ($1, $2, $3) ON CONFLICT (vivint_id) DO UPDATE SET name=EXCLUDED.name, type=EXCLUDED.type;`
					_, err := db.Exec(insertSql, device.Id, device.Name, device.Name)
					if err != nil {
						return err
					}
				}
			}
		}

		logger.Printf("DeviceMap: %+v", deviceMap)
		return nil
	} else {
		return errors.New("could not get users systems, probably not logged in")
	}
}

func main() {
	logger := log.New(os.Stdout, "govivint | ", log.LstdFlags)
	pubnubLogger := log.New(os.Stderr, "pubnub   | ", log.LstdFlags|log.Lshortfile)

	vivintUsername := os.Getenv("VIVINT_USERNAME")
	vivintPassword := os.Getenv("VIVINT_PASSWORD")
	postgresHost := os.Getenv("POSTGRES_HOST")
	postgresUser := os.Getenv("POSTGRES_USER")
	postgresDb := os.Getenv("POSTGRES_DB")
	postgresPassword := os.Getenv("POSTGRES_PASSWORD")
	deviceUuid := os.Getenv("DEVICE_UUID")

	logger.Println("Username", vivintUsername)
	logger.Println("Password", vivintPassword)
	logger.Println("PG Host", postgresHost)
	logger.Println("PG User", postgresUser)
	logger.Println("PG DB", postgresDb)
	logger.Println("PG PASS", postgresPassword)
	logger.Println("Device UUID", deviceUuid)

	connStr := fmt.Sprintf("host=%s user=%s dbname=%s password=%s sslmode=disable", postgresHost, postgresUser, postgresDb, postgresPassword)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		logger.Fatal(err)
	}
	defer db.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}

	client := &http.Client{
		Jar: jar,
	}

	config := pubnub.NewConfig()
	// SubscribeKey from Admin Portal
	config.SubscribeKey = PnSubscribeKey
	// Reconnection policy selection
	config.PNReconnectionPolicy = pubnub.PNExponentialPolicy
	// UUID to be used as a device identifier, a default UUID is generated if not passed
	config.UUID = deviceUuid
	// I don't use non-subscribe requests, so we don't need any workers
	config.MaxWorkers = 0
	config.Log = pubnubLogger

	pn := pubnub.NewPubNub(config)

	logger.Printf("Using UUID: %s", config.UUID)

	listener := pubnub.NewListener()
	go func() {
		for {
			logger.Println("===========================------------------------ Begin listener loop ------------------------ ===========================")
			select {
			case signal := <-listener.Signal:
				logger.Println(fmt.Sprintf("signal.Channel: %s", signal.Channel))
				logger.Println(fmt.Sprintf("signal.Subscription: %s", signal.Subscription))
				logger.Println(fmt.Sprintf("signal.Message: %s", signal.Message))
				logger.Println(fmt.Sprintf("signal.Publisher: %s", signal.Publisher))
				logger.Println(fmt.Sprintf("signal.Timetoken: %d", signal.Timetoken))
			case status := <-listener.Status:
				switch status.Category {
				case pubnub.PNDisconnectedCategory:
					// this is the expected category for an unsubscribe. This means there
					// was no error in unsubscribing from everything
					logger.Println("pubnub.PNDisconnectedCategory: this is the expected category for an unsubscribe. This means there was no error in unsubscribing from everything")
				case pubnub.PNConnectedCategory:
					// this is expected for a subscribe, this means there is no error or issue whatsoever
					logger.Println("pubnub.PNConnectedCategory: this is expected for a subscribe, this means there is no error or issue whatsoever")
				case pubnub.PNReconnectedCategory:
					// this usually occurs if subscribe temporarily fails but reconnects. This means
					// there was an error but there is no longer any issue
					logger.Println("pubnub.PNReconnectedCategory: this usually occurs if subscribe temporarily fails but reconnects. This means there was an error but there is no longer any issue")
				case pubnub.PNAccessDeniedCategory:
					// this means that PAM does allow this client to subscribe to this
					// channel and channel group configuration. This is another explicit error
					logger.Println("pubnub.PNAccessDeniedCategory: this means that PAM does allow this client to subscribe to this channel and channel group configuration. This is another explicit error")
				}
			case message := <-listener.Message:
				logger.Println(fmt.Sprintf("message.Channel: %s", message.Channel))
				logger.Println(fmt.Sprintf("message.Subscription: %s", message.Subscription))
				logger.Println(fmt.Sprintf("message.Message: %+v", message.Message))
				logger.Println(fmt.Sprintf("message.Publisher: %s", message.Publisher))
				logger.Println(fmt.Sprintf("message.Timetoken: %d", message.Timetoken))

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

							insertSql := `INSERT INTO vivint_event(devices, data) VALUES ($1, $2);`
							_, err = db.Exec(insertSql, string(devicesString), string(messageString))
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
					insertSql := `INSERT INTO vivint_event(data) VALUES ($1);`
					_, err = db.Exec(insertSql, string(messageString))
					if err != nil {
						logger.Fatal(err)
					}
					logger.Println("---------------================== End Message ==================---------------")
				}

			case presence := <-listener.Presence:
				logger.Println(fmt.Sprintf("presence.Event: %s", presence.Event))
				logger.Println(fmt.Sprintf("presence.Channel: %s", presence.Channel))
				logger.Println(fmt.Sprintf("presence.Subscription: %s", presence.Subscription))
				logger.Println(fmt.Sprintf("presence.Timetoken: %d", presence.Timetoken))
				logger.Println(fmt.Sprintf("presence.Occupancy: %d", presence.Occupancy))
			case uuidEvent := <-listener.UUIDEvent:
				logger.Println(fmt.Sprintf("uuidEvent.Channel: %s", uuidEvent.Channel))
				logger.Println(fmt.Sprintf("uuidEvent.SubscribedChannel: %s", uuidEvent.SubscribedChannel))
				logger.Println(fmt.Sprintf("uuidEvent.Event: %s", uuidEvent.Event))
				logger.Println(fmt.Sprintf("uuidEvent.UUID: %s", uuidEvent.UUID))
				logger.Println(fmt.Sprintf("uuidEvent.Description: %s", uuidEvent.Description))
				logger.Println(fmt.Sprintf("uuidEvent.Timestamp: %s", uuidEvent.Timestamp))
				logger.Println(fmt.Sprintf("uuidEvent.Name: %s", uuidEvent.Name))
				logger.Println(fmt.Sprintf("uuidEvent.ExternalID: %s", uuidEvent.ExternalID))
				logger.Println(fmt.Sprintf("uuidEvent.ProfileURL: %s", uuidEvent.ProfileURL))
				logger.Println(fmt.Sprintf("uuidEvent.Email: %s", uuidEvent.Email))
				logger.Println(fmt.Sprintf("uuidEvent.Updated: %s", uuidEvent.Updated))
				logger.Println(fmt.Sprintf("uuidEvent.ETag: %s", uuidEvent.ETag))
				logger.Println(fmt.Sprintf("uuidEvent.Custom: %v", uuidEvent.Custom))
			case channelEvent := <-listener.ChannelEvent:
				logger.Println(fmt.Sprintf("channelEvent.Channel: %s", channelEvent.Channel))
				logger.Println(fmt.Sprintf("channelEvent.SubscribedChannel: %s", channelEvent.SubscribedChannel))
				logger.Println(fmt.Sprintf("channelEvent.Event: %s", channelEvent.Event))
				logger.Println(fmt.Sprintf("channelEvent.Channel: %s", channelEvent.Channel))
				logger.Println(fmt.Sprintf("channelEvent.Description: %s", channelEvent.Description))
				logger.Println(fmt.Sprintf("channelEvent.Timestamp: %s", channelEvent.Timestamp))
				logger.Println(fmt.Sprintf("channelEvent.Updated: %s", channelEvent.Updated))
				logger.Println(fmt.Sprintf("channelEvent.ETag: %s", channelEvent.ETag))
				logger.Println(fmt.Sprintf("channelEvent.Custom: %v", channelEvent.Custom))
			case membershipEvent := <-listener.MembershipEvent:
				logger.Println(fmt.Sprintf("membershipEvent.Channel: %s", membershipEvent.Channel))
				logger.Println(fmt.Sprintf("membershipEvent.SubscribedChannel: %s", membershipEvent.SubscribedChannel))
				logger.Println(fmt.Sprintf("membershipEvent.Event: %s", membershipEvent.Event))
				logger.Println(fmt.Sprintf("membershipEvent.Channel: %s", membershipEvent.Channel))
				logger.Println(fmt.Sprintf("membershipEvent.UUID: %s", membershipEvent.UUID))
				logger.Println(fmt.Sprintf("membershipEvent.Description: %s", membershipEvent.Description))
				logger.Println(fmt.Sprintf("membershipEvent.Timestamp: %s", membershipEvent.Timestamp))
				logger.Println(fmt.Sprintf("membershipEvent.Custom: %v", membershipEvent.Custom))
			case messageActionsEvent := <-listener.MessageActionsEvent:
				logger.Println(fmt.Sprintf("messageActionsEvent.Channel: %s", messageActionsEvent.Channel))
				logger.Println(fmt.Sprintf("messageActionsEvent.SubscribedChannel: %s", messageActionsEvent.SubscribedChannel))
				logger.Println(fmt.Sprintf("messageActionsEvent.Event: %s", messageActionsEvent.Event))
				logger.Println(fmt.Sprintf("messageActionsEvent.Data.ActionType: %s", messageActionsEvent.Data.ActionType))
				logger.Println(fmt.Sprintf("messageActionsEvent.Data.ActionValue: %s", messageActionsEvent.Data.ActionValue))
				logger.Println(fmt.Sprintf("messageActionsEvent.Data.ActionTimetoken: %s", messageActionsEvent.Data.ActionTimetoken))
				logger.Println(fmt.Sprintf("messageActionsEvent.Data.MessageTimetoken: %s", messageActionsEvent.Data.MessageTimetoken))
			case file := <-listener.File:
				logger.Println(fmt.Sprintf("file.File.PNMessage.Text: %s", file.File.PNMessage.Text))
				logger.Println(fmt.Sprintf("file.File.PNFile.Name: %s", file.File.PNFile.Name))
				logger.Println(fmt.Sprintf("file.File.PNFile.ID: %s", file.File.PNFile.ID))
				logger.Println(fmt.Sprintf("file.File.PNFile.URL: %s", file.File.PNFile.URL))
				logger.Println(fmt.Sprintf("file.Channel: %s", file.Channel))
				logger.Println(fmt.Sprintf("file.Timetoken: %d", file.Timetoken))
				logger.Println(fmt.Sprintf("file.SubscribedChannel: %s", file.SubscribedChannel))
				logger.Println(fmt.Sprintf("file.Publisher: %s", file.Publisher))
			}
			logger.Print("===========================------------------------ End listener loop ------------------------ ===========================\n\n\n\n")
		}
	}()

	pn.AddListener(listener)

	loginInfo, loginErr := executeLogin(logger, client, vivintUsername, vivintPassword)
	if loginErr != nil {
		logger.Fatalf("Update devices error: %s, attempted another login which also resulted in an error: %s", err, loginErr)
	}

	pnChannel := PnChannel + "#" + loginInfo.Users.MessageBroadcastChannel
	logger.Println("pnChannel", pnChannel)
	pn.Subscribe().Channels([]string{pnChannel}).Execute()

	go func() {
		for {
			logger.Println("---------------================== Executing New Login ==================---------------")
			newLoginInfo, loginErr := executeLogin(logger, client, vivintUsername, vivintPassword)
			if loginErr != nil {
				logger.Fatalf("Update devices error: %s, attempted another login which also resulted in an error: %s", err, loginErr)
			}

			newPnChannel := PnChannel + "#" + newLoginInfo.Users.MessageBroadcastChannel

			logger.Println("--- Old Pn Channel", pnChannel)
			logger.Println("--- New Pn Channel", newPnChannel)
			if newPnChannel != pnChannel {
				logger.Fatal("PnChannel changed with new login... probably worth investigating")
			}

			time.Sleep(1 * time.Minute)
		}
	}()

	go func() {
		for {
			logger.Println("---------------================== Updating Devices ==================---------------")
			err = updateDevices(logger, client, db, loginInfo)
			if err != nil {
				logger.Println("Unable to update devices. Assuming that this is a login issue and ignoring this issue")
			}

			time.Sleep(5 * time.Minute)
		}
	}()

	// In order to keep our devices up to date we will re-login/re-update every 5 minutes... this is probably over zealous because how often do people add new devices to their home...
	// go func() {
	// 	var loginInfo LoginInfo
	// 	for {
	// 		err = updateDevices(logger, client, db, loginInfo)
	// 		if err != nil {
	// 			updatedLoginInfo, loginErr := executeLogin(logger, client, vivintUsername, vivintPassword)
	// 			if loginErr != nil {
	// 				logger.Fatalf("Update devices error: %s, attempted another login which also resulted in an error: %s", err, loginErr)
	// 			}
	//
	// 			loginInfo = updatedLoginInfo
	// 			updateDevicesErr := updateDevices(logger, client, db, loginInfo)
	// 			if updateDevicesErr != nil {
	// 				logger.Fatalf("Unable to update devices after re-logging in: %s", updateDevicesErr)
	// 			}
	// 		}
	//
	// 		pnChannel := PnChannel + "#" + loginInfo.Users.MessageBroadcastChannel
	// 		logger.Println("pnChannel", pnChannel)
	// 		pn.Subscribe().Channels([]string{pnChannel}).WithPresence(true).Execute()
	//
	// 		time.Sleep(1 * time.Minute)
	// 	}
	// }()

	// go func() {
	// 	for {
	// 		// TODO: Why is this the only way to keep receiving messages
	// 		logger.Println("~~~~~~~~~~~~~~~~~~~~~~ Resubscribing ~~~~~~~~~~~~~~~~~~~~~~")
	// 		pnChannel := PnChannel + "#" + loginInfo.Users.MessageBroadcastChannel
	// 		logger.Println("pnChannel", pnChannel)
	// 		logger.Printf("Subscribed channels: %+v", pn.GetSubscribedChannels())
	// 		pn.Subscribe().Channels([]string{pnChannel}).WithPresence(true).Execute()
	// 		time.Sleep(15 * time.Second)
	// 	}
	// }()

	// TODO: This will just wait forever since we never call wg.Done() anywhere ... we should wait on something useful
	var wg sync.WaitGroup
	wg.Add(1)

	wg.Wait()
}
