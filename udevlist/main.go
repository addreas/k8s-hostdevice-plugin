package main

import (
	"context"
	"fmt"

	udev "github.com/jochenvg/go-udev"
)

func main() {
	u := udev.Udev{}
	e := u.NewEnumerate()
	e.AddMatchIsInitialized()
	ds, _ := e.Devices()
	fmt.Println("Devices:")
	for _, d := range ds {
		printDevice(d)
		fmt.Printf("---")
	}

	mon := u.NewMonitorFromNetlink("udev")
	devices, err := mon.DeviceChan(context.Background())
	if err != nil {
		fmt.Printf("err: %s", err)
	}
	for {

		select {
		case dev := <-devices:
			fmt.Println("Update: ")
			printDevice(dev)

		}
	}
}

func printDevice(d *udev.Device) {
	fmt.Printf("Sysname: %s\n", d.Syspath())
	fmt.Printf("Devpath: %s\n", d.Devpath())

	for l, _ := range d.Devlinks() {
		fmt.Printf("Link: %s\n", l)
	}

	for tk, tv := range d.Tags() {
		fmt.Printf("Tag: %s, Value: %s\n", tk, tv)
	}

	for pk, pv := range d.Properties() {
		fmt.Printf("Property: %s, Value: %s\n", pk, pv)
	}
}
