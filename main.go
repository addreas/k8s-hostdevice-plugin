package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/jochenvg/go-udev"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func createDevicePlugins(config Config) (map[string]*HostDevicePlugin, error) {
	dps := make(map[string]*HostDevicePlugin)
	var u udev.Udev
	os.MkdirAll(path.Join(pluginapi.DevicePluginPath, config.SocketPrefix), 0755)

	udevs, err := u.NewEnumerate().Devices()
	if err != nil {
		return nil, fmt.Errorf("failed to list udev devices: %s", err)
	}

	for resourceName, devconf := range config.Devices {
		safeName := strings.Replace(resourceName, "/", "-", -1)
		socketPath := path.Join(pluginapi.DevicePluginPath, config.SocketPrefix, safeName+".sock")

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
		dp := NewHostDevicePlugin(devs, socketPath, resourceName, &devconf)

		if err := dp.Start(); err != nil {
			return dps, fmt.Errorf("failed to start device plugin: %s", err)
		}

		if err := dp.Register(pluginapi.KubeletSocket, resourceName); err != nil {
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

	var u udev.Udev
	mon := u.NewMonitorFromNetlink("udev")
	devices, err := mon.DeviceChan(context.Background())
	if err != nil {
		klog.Fatalf("failed to creat udev monitor chan: %s\n", err)
	}

	restart := false
L:
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

		case dev := <-devices:
			klog.Infof("device channel: %+v", dev)
			restart = true

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
				break L
			}
		}
	}
}