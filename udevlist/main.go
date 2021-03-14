package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pilebones/go-udev/crawler"
	"github.com/pilebones/go-udev/netlink"
)

func main() {
	info(nil)
	monitor(nil)
}

func info(matcher netlink.Matcher) {
	log.Println("Get existing devices...")

	queue := make(chan crawler.Device)
	errors := make(chan error)
	quit := crawler.ExistingDevices(queue, errors, matcher)

	// Signal handler to quit properly monitor mode
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-signals
		log.Println("Exiting info mode...")
		close(quit)
		os.Exit(0)
	}()

	// Handling message from queue
	for {
		select {
		case device, more := <-queue:
			if !more {
				log.Printf("Finished processing existing devices\n")
				return
			}
			log.Println("Detect device at", device.KObj, "with env", device.Env)
		case err := <-errors:
			log.Println("ERROR:", err)
		}
	}
}

// monitor run monitor mode
func monitor(matcher netlink.Matcher) {
	log.Println("Monitoring UEvent kernel message to user-space...")

	conn := new(netlink.UEventConn)
	if err := conn.Connect(netlink.UdevEvent); err != nil {
		log.Fatalln("Unable to connect to Netlink Kobject UEvent socket")
	}
	defer conn.Close()

	queue := make(chan netlink.UEvent)
	errors := make(chan error)
	quit := conn.Monitor(queue, errors, matcher)

	// Signal handler to quit properly monitor mode
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-signals
		log.Println("Exiting monitor mode...")
		close(quit)
		os.Exit(0)
	}()

	for {
		select {
		case uevent := <-queue:
			device := crawler.Device{
				KObj: uevent.KObj,
				Env:  uevent.Env,
			}
			log.Println("Detect device at", device.KObj, "with env", device.Env)
		case err := <-errors:
			log.Println("ERROR:", err)
		}
	}

}
