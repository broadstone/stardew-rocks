package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/nictuku/stardew-rocks/parser"
	"github.com/nictuku/stardew-rocks/stardb"
	"github.com/nictuku/stardew-rocks/view"

	"github.com/streadway/amqp"
)

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

func wwwDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = string(filepath.Separator)
	}
	return filepath.Clean(filepath.Join(home, "www"))
}

func main() {
	conn, err := amqp.Dial("amqp://guest:guest@amqp.stardew.rocks:5672/")
	failOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")
	defer ch.Close()
	for _, exc := range []string{"SaveGameInfo-1", "OtherFiles-1"} {
		err = ch.ExchangeDeclare(
			exc,      // name
			"fanout", // type
			false,    // durable
			false,    // auto-deleted
			false,    // internal
			false,    // no-wait
			nil,      // arguments
		)

		failOnError(err, "Failed to declare an exchange")
	}
	q, err := ch.QueueDeclare(
		"",    // name
		false, // durable
		false, // delete when usused
		true,  // exclusive
		false, // no-wait
		nil,   // arguments
	)
	failOnError(err, "Failed to declare a queue")

	err = ch.QueueBind(
		q.Name,         // queue name
		"",             // routing key
		"OtherFiles-1", // exchange
		false,
		nil)
	failOnError(err, "Failed to bind a queue")

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	failOnError(err, "Failed to register a consumer")

	count := 0

	farmMap := parser.LoadFarmMap()

	go func() {
		for d := range msgs {
			count++
			var reader io.Reader = bytes.NewReader(d.Body)
			// The content is usually gzip encoded by we don't have to worry about that.
			// Apparently rabbitMQ or the Go library will decompress it transparently.
			// d.ContentEncoding == "gzip" {
			saveGame, err := parser.ParseSaveGame(reader)
			if err != nil {
				log.Print("Error parsing saved game:", err)
				continue
			}
			if saveGame.Player.Name == "" {
				log.Print("Ignoring save with blank player name")
				continue
			}

			name := path.Base(path.Clean(saveGame.Player.Name)) // please don't hacko me mister

			ts := time.Now()

			farm, _, err := stardb.FindFarm(stardb.FarmCollection, saveGame.UniqueIDForThisGame, saveGame.Player.Name, saveGame.Player.FarmName)
			if err != nil {
				log.Print("Error fetching farm ID:", err)
				continue
			}

			// DEPRECATED filesystem write.
			//
			// Write the save game, then write the screenshot.
			// TODO: deal with races and conflicts.
			saveFile := path.Join(wwwDir(), "saveGames", fmt.Sprintf("%v-%d.xml", name, ts.Unix()))
			sf, err := os.OpenFile(saveFile, os.O_CREATE|os.O_WRONLY, 0666)
			if err != nil {
				log.Printf("Error opening saveGames %v: %v", saveFile, err)
				continue
			}
			if _, err := sf.Write(d.Body); err != nil {
				log.Printf("Failed to write save file at %v: %v", saveFile, err)
				continue
			} else {
				log.Printf("Wrote saveGame file %v", saveFile)
			}
			sf.Close()

			// GridFS XML save file write.
			// TODO: broken saves (length 0)
			if err := stardb.WriteSaveFile(farm, d.Body, ts); err != nil {
				log.Print("write save file:", err)
				continue
			}
			// The save file is the most critical and it's been updated, so we should be fine.
			if err := stardb.UpdateFarmTime(farm.InternalID, ts); err != nil {
				log.Print("update farm time:", err)
				continue
			}

			// DEPRECATED filesystem write.
			mapFile := path.Join(wwwDir(), fmt.Sprintf("map-%v-%d.png", name, ts.Unix()))
			f, err := os.OpenFile(mapFile, os.O_CREATE|os.O_WRONLY, 0666)
			if err != nil {
				log.Printf("Error opening screenshot file %v: %v", mapFile, err)
				continue
			}
			view.WriteImage(farmMap, saveGame, f)
			f.Close()
			log.Printf("Wrote map file %v", mapFile)

			// GridFs screenshot write.
			fs, err := stardb.NewScreenshotWriter(farm, ts)
			if err != nil {
				log.Print("Error writing grid screenshot:", err)
				continue
			}

			if err := view.WriteImage(farmMap, saveGame, fs); err != nil {
				log.Print(err)
				fs.Close()
				continue
			}
			fs.Close()
			log.Printf("Wrote grid map file %v", farm.ScreenshotPath())

		}
		log.Printf("Total messages so far: %d", count)

	}()

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	select {}
}
