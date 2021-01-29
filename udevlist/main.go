package main

import (
	"fmt"

	udev "github.com/jochenvg/go-udev"
)

func main() {
	u := udev.Udev{}
	e := u.NewEnumerate()
	e.AddMatchIsInitialized()
	ds, _ := e.Devices()
	for _, d := range ds {
		hasLink := false
		for l, _ := range d.Devlinks() {
			fmt.Printf("Link: %s\n", l)
			hasLink = true
		}
		if hasLink {
			fmt.Printf("Sysname: %s\n", d.Syspath())
			fmt.Printf("Devpath: %s\n", d.Devpath())

			for tk, tv := range d.Tags() {
				fmt.Printf("Tag: %s, Value: %s\n", tk, tv)
			}

			for pk, pv := range d.Properties() {
				fmt.Printf("Property: %s, Value: %s\n", pk, pv)
			}
		}
	}
}
