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

	"github.com/google/uuid"
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
				Id   int      `json:"_id"`
				Type string   `json:"t"`
				Name string   `json:"n"`
				Vd   []string `json:"vd"`
			} `json:"d"`
		} `json:"par"`
	} `json:"system"`
}

func main() {
	vivintUsername := os.Getenv("VIVINT_USERNAME")
	vivintPassword := os.Getenv("VIVINT_PASSWORD")
	postgresHost := os.Getenv("POSTGRES_HOST")
	postgresUser := os.Getenv("POSTGRES_USER")
	postgresDb := os.Getenv("POSTGRES_DB")
	postgresPassword := os.Getenv("POSTGRES_PASSWORD")

	connStr := fmt.Sprintf("host=%s user=%s dbname=%s password=%s sslmode=disable", postgresHost, postgresUser, postgresDb, postgresPassword)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var now time.Time
	err = db.QueryRow("SELECT NOW()").Scan(&now)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Db Now: %s", now)

	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}

	jsonString := []byte(fmt.Sprintf(`{"username": "%s", "password": "%s"}`, vivintUsername, vivintPassword))
	client := &http.Client{
		Jar: jar,
	}

	loginRequest, err := http.NewRequest("POST", VivintSkyEndpoint+"login", bytes.NewBuffer(jsonString))
	loginRequest.Header.Set("Content-Type", "application/json")

	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		panic(err)
	}
	defer loginResponse.Body.Close()

	// fmt.Println("login response headers", resp.Header)
	// body, _ := ioutil.ReadAll(resp.Body)
	// fmt.Println("login response body:", string(body))

	loginInfo := LoginInfo{}
	json.NewDecoder(loginResponse.Body).Decode(&loginInfo)
	log.Printf("LoginInfo %+v", loginInfo)

	for _, system := range loginInfo.Users.System {
		log.Printf("Getting panel devices: %d", system.PanelId)
		devicesRequest, err := client.Get(VivintSkyEndpoint + "systems/" + strconv.FormatInt(system.PanelId, 10) + "?includerules=false")
		if err != nil {
			log.Fatal(err)
		}
		defer devicesRequest.Body.Close()

		systemInfo := SystemInfo{}
		json.NewDecoder(devicesRequest.Body).Decode(&systemInfo)
		log.Printf("System Info: %+v", systemInfo)
	}

	// config := pubnub.NewConfig()
	config := pubnub.NewConfig()
	config.SubscribeKey = PnSubscribeKey
	config.PNReconnectionPolicy = pubnub.PNLinearPolicy
	config.UUID = uuid.New().String()
	pn := pubnub.NewPubNub(config)

	log.Printf("Using UUID: %s", config.UUID)

	listener := pubnub.NewListener()
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		for {
			log.Println("Listener running")
			select {
			case status := <-listener.Status:
				log.Printf("status %+v", status)
				switch status.Category {
				case pubnub.PNDisconnectedCategory:
					// This event happens when radio / connectivity is lost
					log.Println("PNDisconnectedCategory")
				case pubnub.PNConnectedCategory:
					// Connect event. You can do stuff like publish, and know you'll get it.
					// Or just use the connected event to confirm you are subscribed for
					// UI / internal notifications, etc
					log.Println("PNConnectedCategory")
				case pubnub.PNReconnectedCategory:
					// Happens as part of our regular operation. This event happens when
					// radio / connectivity is lost, then regained.
					log.Println("PNReconnectedCategory")
				}
			case message := <-listener.Message:
				// Handle new message stored in message.message
				if message.Channel != "" {
					log.Println("message.Channel", message.Channel)
					// Message has been received on channel group stored in
					// message.Channel
				} else {
					log.Println("message.Subscription", message.Subscription)
					// Message has been received on channel stored in
					// message.Subscription
				}

				log.Println("---------------================== Start Message ==================---------------")
				if msg, ok := message.Message.(map[string]interface{}); ok {
					log.Println("msg", msg["da"])

					if da, ok := msg["da"].(map[string]interface{}); ok {
						log.Println("da", da)
						log.Println("da[d]", da["d"])

						if d, ok := da["d"].([]map[string]interface{}); ok {
							log.Println("d", d)
						} else {
							log.Println("not []map[string]interface{}")
						}
					}

					// log.Println("msg[\"da\"][\"d\"][0][\"_id\"]", msg["da"]["d"][0]["_id"])
				}

				b, err := json.Marshal(message.Message)
				if err != nil {
					log.Fatal(err)
				}

				log.Println(string(b))

				log.Println("---------------================== End Start Message ==================---------------")

				// log.Println(message.Message)

				// log.Println(message.Subscription)
				// log.Println(message.Timetoken)

				/*
				   log the following items with your favorite logger
				       - message.Message
				       - message.Subscription
				       - message.Timetoken
				*/

				// donePublish <- true
			case <-listener.Presence:
				// handle presence
				log.Println("Presence")
			}
		}
	}()

	pn.AddListener(listener)

	pnChannel := PnChannel + "#" + loginInfo.Users.MessageBroadcastChannel
	log.Println("pnChannel", pnChannel)
	pn.Subscribe().Channels([]string{pnChannel}).Execute()

	// authUserResp, err := client.Get(VivintSkyEndpoint + "authuser")
	// if err != nil {
	// 	panic(err)
	// }
	// defer authUserResp.Body.Close()

	// fmt.Println("authUser response status", authUserResp.Status)
	// fmt.Println("authUser response headers", authUserResp.Header)
	// authUserBody, _ := ioutil.ReadAll(authUserResp.Body)
	// fmt.Println("authUser response body:", string(authUserBody))

	// TODO: This will just wait forever since we never call wg.Done() anywhere ... we should wait on something useful
	wg.Wait()
}
