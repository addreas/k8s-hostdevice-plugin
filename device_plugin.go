package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/jochenvg/go-udev"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
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

func createDevicePlugins(config Config) (map[string]*Stub, error) {
	dps := make(map[string]*Stub)
	var u udev.Udev
	socketDir := pluginapi.DevicePluginPath + config.SocketPrefix + "/"
	os.MkdirAll(socketDir, 0755)

	udevs, err := u.NewEnumerate().Devices()
	if err != nil {
		return nil, fmt.Errorf("failed to list udev devices: %s", err)
	}

	for resourceName, devconf := range config.Devices {
		socketPath := fmt.Sprintf("%s%s.sock", socketDir, strings.Replace(resourceName, "/", "-", -1))

		devs := []*pluginapi.Device{}

		for _, ud := range udevs {
			if !devconf.matchesProperties(ud) {
				continue
			}

			devs = append(devs, &pluginapi.Device{
				ID:     ud.Syspath(),
				Health: pluginapi.Healthy,
			})
		}

		klog.Infof("Setting up device %s with socket path %s and devices %s", resourceName, socketPath, devs)
		dp := NewDevicePluginStub(devs, socketPath, resourceName, false, false)

		dp.SetAllocFunc(func(r *pluginapi.AllocateRequest, devs map[string]pluginapi.Device) (*pluginapi.AllocateResponse, error) {
			var responses pluginapi.AllocateResponse
			for _, req := range r.ContainerRequests {
				response := &pluginapi.ContainerAllocateResponse{}
				for _, requestID := range req.DevicesIDs {
					dev, ok := devs[requestID]
					if !ok {
						return nil, fmt.Errorf("invalid allocation request with non-existing device %s", requestID)
					}

					if dev.Health != pluginapi.Healthy {
						return nil, fmt.Errorf("invalid allocation request with unhealthy device: %s", requestID)
					}

					response.Devices = append(response.Devices, &pluginapi.DeviceSpec{
						ContainerPath: devconf.ContainerPath,
						HostPath:      dev.ID,
						Permissions:   devconf.Permissions,
					})
				}
				responses.ContainerResponses = append(responses.ContainerResponses, response)
			}

			return &responses, nil
		})

		if err := dp.Start(); err != nil {
			return dps, fmt.Errorf("failed to start device plugin: %s", err)
		}

		if err := dp.Register(pluginapi.KubeletSocket, resourceName, socketDir); err != nil {
			return dps, fmt.Errorf("failed to register device plugin: %s", err)
		}

		dps[resourceName] = dp
	}

	return dps, nil
}

func main() {
	klog.Infoln("Starting FS watcher.")
	kubeletWatcher, err := newFSWatcher(pluginapi.DevicePluginPath)
	if err != nil {
		klog.Fatalf("failed to start fswatcher: %s\n", err)
	}
	defer kubeletWatcher.Close()

	klog.Infoln("Starting OS watcher.")
	sigWatcher := newOSWatcher(syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	var config Config
	if err := config.load(); err != nil {
		klog.Fatalf("failed to load config: %s\n", err)
	}

	dps, err := createDevicePlugins(config)
	if err != nil {
		klog.Fatalf("failed to create device plugins: %s\n", err)
	}

	restart := false
	for {
		if restart {
			restart = false
			for _, dp := range dps {
				dp.Stop()
			}

			config.load()
			dps, err = createDevicePlugins(config)
			if err != nil {
				klog.Fatalf("failed to create device plugins: %s\n", err)
			}
		}

		select {
		case event := <-kubeletWatcher.Events:
			if event.Name == pluginapi.KubeletSocket && event.Op&fsnotify.Create == fsnotify.Create {
				klog.Infof("inotify: %s created, restarting.", pluginapi.KubeletSocket)
				restart = true
			}

		case err := <-kubeletWatcher.Errors:
			klog.Infof("inotify: %s", err)

		case s := <-sigWatcher:
			switch s {
			case syscall.SIGHUP:
				klog.Infoln("Received SIGHUP, restarting.")
				restart = true
			default:
				klog.Infof("Received signal \"%v\", shutting down.", s)
				for _, dp := range dps {
					dp.Stop()
				}
			}
		}
	}
}
