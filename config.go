package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/jochenvg/go-udev"
	"k8s.io/klog/v2"
)

// Config specifies common options
type Config struct {
	SocketPrefix string                  `json:"socketPrefix"`
	Devices      map[string]DeviceConfig `json:"devices"`
}

// DeviceConfig specifies options specific to a single device/resource
type DeviceConfig struct {
	ContainerPath   string            `json:"containerPath"`
	Permissions     string            `json:"permissions"`
	MatchProperties map[string]string `json:"matchProperties"`
}

func (config *Config) load() error {
	klog.Infoln("Reading /k8s-hostdevice-plugin/config.json")
	raw, err := ioutil.ReadFile("/k8s-hostdevice-plugin/config.json")
	if err != nil {
		return fmt.Errorf("failed to read config file: %s", err)
	}
	err = json.Unmarshal(raw, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config json: %s", err)
	}
	klog.Infoln("loaded config: ", config)
	return nil
}

func (devconf *DeviceConfig) matchesProperties(ud *udev.Device) bool {
	for property, value := range devconf.MatchProperties {
		if ud.PropertyValue(property) != value {
			return false
		}
	}
	return true
}
