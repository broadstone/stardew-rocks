// This program watches for recently changed stardew save files and publishes them to the
// Stardew Rocks AMQP server.
package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"path/filepath"

	"github.com/cratonica/trayhost"
	"github.com/fsnotify/fsnotify"
	"github.com/marcsauter/single"
	"github.com/streadway/amqp"
)

func stardewFolder() string {
	return path.Join(os.Getenv("AppData"), "StardewValley/Saves")
}

func allSaveGameInfos() ([]string, error) {
	return filepath.Glob(stardewFolder() + "/*/*")
}

func isDir(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

func relPath(p string) string {
	rel, err := filepath.Rel(stardewFolder(), p)
	if err != nil {
		log.Fatal(err)
	}
	return rel
}

func ignoreFile(p string) bool {
	if filepath.Ext(p) != "" {
		return true
	}
	return false
}

func watchAndPublish(topic *amqp.Channel, cancel chan *amqp.Error) {

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	gameSaves, err := allSaveGameInfos()
	if err != nil {
		log.Fatal(err)
	}

	// Find new folders containing new saved games.
	err = watcher.Add(stardewFolder())
	if err != nil {
		log.Fatal(err)
	}

	watched := map[string]bool{}

	for _, save := range gameSaves {
		if ignoreFile(save) {
			continue
		}
		err = watcher.Add(save)
		if err != nil {
			log.Fatal(err)
		}
		log.Infof("Watching %v", relPath(save))
		watched[save] = true
	}
	stop := make(chan bool, 1)

	go func() {

		for {
			select {
			case <-stop:
				return
			// The game metadata is saved in a temporary file first (Directory/SaveGameInfo_STARDEWVALLEYTMP).
			// If the write works, it renames it to replace the older file.
			// Our job here is to watch for new files being created and written to, and stop watching the ones that get deleted.
			// To avoid crashing the game while it's renaming stuff, we ignore the temp
			// file and watch for when the game finished doing the rename.
			case event := <-watcher.Events:
				filename := event.Name
				// Ignore files with extensions
				if ignoreFile(filename) {
					continue
				}
				log.Debug("file watch:", relPath(filename), event.String())
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					watcher.Remove(filename)
					delete(watched, filename)
					log.Debug("Deleted file watch:", relPath(filename))
				} else if !watched[filename] {
					watcher.Add(filename)
					watched[filename] = true
					log.Debug("Watching %v", relPath(filename))
				}
				if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Rename == fsnotify.Rename || event.Op&fsnotify.Write == fsnotify.Write {
					log.Debug("Found modified file:", relPath(filename))
					switch {
					case isDir(filename):
						continue
					case strings.Contains(path.Base(filename), "SAVETMP"):
						// Ignore the TMP file, only read after it's renamed. This avoid crashes.
						continue
					case strings.Contains(path.Base(filename), "SaveGameInfo"):
						if err := publishSavedGame(topic, filename); err != nil {
							// This is normal. We tried to open the file after it's been renamed.
							log.Debugf("could not publish new save game content: %v", err)
							continue
						}
						log.Info("[x] New save game published")
					default:
						if err := publishOtherFiles(topic, filename); err != nil {
							// This is normal. We tried to open the file after it's been renamed.
							log.Debugf("could not publish content: %v", relPath(filename), err)
							continue
						}
						log.Infof("[x] New detailed game file published")
					}
				}
			case err := <-watcher.Errors:
				log.Error("error:", err)
			}
		}
	}()
	err = <-cancel
	log.Errorf("Channel Error: %v", err)
	stop <- true
	return
}

func main() {

	s := single.New("stardew-rocks-client") // Will exit if already running.
	s.Lock()
	defer s.Unlock()

	// Run on startup. If this fails, aborts.
	// Make sure the user fixes this.
	setupRunOnStartup()

	// Windows tray.
	// EnterLoop must be called on the OS's main thread
	runtime.LockOSThread()

	go func() {
		// TODO: Local page with debug information.
		go http.Serve(localAddr, nil)
		trayhost.SetUrl(fmt.Sprintf("http://%s", localAddr.Addr()))
	}()

	go func() {

		for {

			topic, close, err := rabbitStart()
			if err != nil {
				log.Warning(err)
				time.Sleep(randSleep())
				continue
			}
			// Find when we need to reconnect.
			cc := make(chan *amqp.Error)
			topic.NotifyClose(cc)

			watchAndPublish(topic, cc)
			close()
			// Don't retry too fast.
			time.Sleep(randSleep())

		}

	}()
	// Enter the host system's event loop
	trayhost.EnterLoop("Stardew Rocks", iconData)
	log.Warningf("Exiting..")
}

func randSleep() time.Duration {
	n, _ := rand.Int(rand.Reader, big.NewInt(60))
	return time.Duration(n.Int64()) * time.Second
}
