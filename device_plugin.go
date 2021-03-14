package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/pilebones/go-udev/crawler"
	"github.com/pilebones/go-udev/netlink"
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

func (config *Config) allocFuncFor(resourceName string) stubAllocFunc {
	devconf := config.Devices[resourceName]
	return func(r *pluginapi.AllocateRequest, devs map[string]pluginapi.Device) (*pluginapi.AllocateResponse, error) {
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

				if devconf.Permissions == "" {
					return nil, fmt.Errorf("permissions for device cannot be empty for device %s", requestID)
				}

				response.Devices = append(response.Devices, &pluginapi.DeviceSpec{
					HostPath:      dev.ID + "/device",
					ContainerPath: devconf.ContainerPath,
					Permissions:   devconf.Permissions,
				})
			}
			responses.ContainerResponses = append(responses.ContainerResponses, response)
		}

		return &responses, nil
	}
}

func getExisting(matcher netlink.Matcher) []*pluginapi.Device {
	queue := make(chan crawler.Device)
	errors := make(chan error)
	crawler.ExistingDevices(queue, errors, matcher)

	devices := make([]*pluginapi.Device, 10)
	for {
		select {
		case device, more := <-queue:
			devices = append(devices, &pluginapi.Device{
				ID:     device.KObj,
				Health: pluginapi.Healthy,
			})
			if !more {
				return devices
			}
			log.Println("Detect device at", device.KObj, "with env", device.Env)
		case err := <-errors:
			log.Println("ERROR:", err)
		}
	}
}

func runDevicePlugins(ctx context.Context, config Config) chan error {
	socketDir := pluginapi.DevicePluginPath + config.SocketPrefix + "/"
	os.MkdirAll(socketDir, 0755)

	outerErr := make(chan error)

	for resourceName, devconf := range config.Devices {
		socketPath := fmt.Sprintf("%s%s.sock", socketDir, strings.Replace(resourceName, "/", "-", -1))

		matcher := &netlink.RuleDefinition{
			Env: devconf.MatchProperties,
		}
		devices := getExisting(matcher)

		klog.Infof("Setting up device %s with socket path %s and devices %s", resourceName, socketPath, devices)
		dp := NewDevicePluginStub(devices, socketPath, resourceName, false, false)
		dp.SetAllocFunc(config.allocFuncFor(resourceName))

		if err := dp.Start(); err != nil {
			outerErr <- fmt.Errorf("failed to start device plugin: %s", err)
		}

		if err := dp.Register(pluginapi.KubeletSocket, resourceName, socketDir); err != nil {
			outerErr <- fmt.Errorf("failed to register device plugin: %s", err)
		}

		conn := new(netlink.UEventConn)
		defer conn.Close()
		update := make(chan netlink.UEvent)
		errors := make(chan error)
		quit := conn.Monitor(update, errors, matcher)

		go func() {
			for {
				select {
				case <-ctx.Done():
					dp.Stop()
					close(quit)
				case <-update:
					dp.Update(getExisting(matcher))
				case err := <-errors:
					klog.Errorf("received error from udev watch: %s\n", err)
					outerErr <- err
				}
			}
		}()
	}

	return outerErr
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

	state := "run"
	ctx, cancel := context.WithCancel(context.Background())
	errors := runDevicePlugins(ctx, config)

	for state != "stop" {
		if state == "restart" {
			state = "run"
			cancel()
			ctx, cancel = context.WithCancel(context.Background())
			config.load()
			errors = runDevicePlugins(ctx, config)
		}

		select {
		case event := <-kubeletWatcher.Events:
			if event.Name == pluginapi.KubeletSocket && event.Op&fsnotify.Create == fsnotify.Create {
				klog.Infof("inotify: %s created, restarting.", pluginapi.KubeletSocket)
				state = "restart"
			}

		case err := <-kubeletWatcher.Errors:
			klog.Infof("inotify: %s", err)

		case err := <-errors:
			klog.Errorf("error in device plugin: %s", err)

		case s := <-sigWatcher:
			switch s {
			case syscall.SIGHUP:
				klog.Infoln("Received SIGHUP, restarting.")
				state = "restart"
			default:
				klog.Infof("Received signal \"%v\", shutting down.", s)
				state = "stop"
			}
		}
	}
	cancel()
}
