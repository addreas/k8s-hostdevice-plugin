package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jochenvg/go-udev"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func createDevicePlugins(config Config) (map[string]*HostDevicePlugin, error) {
	dps := make(map[string]*HostDevicePlugin)
	os.MkdirAll(path.Join(pluginapi.DevicePluginPath, config.SocketPrefix), 0755)

	for resourceName, devconf := range config.Devices {
		safeName := strings.Replace(resourceName, "/", "-", -1)
		socketPath := path.Join(pluginapi.DevicePluginPath, config.SocketPrefix, safeName+".sock")

		devs, err := devconf.getPluginDevices()
		if err != nil {
			return nil, fmt.Errorf("failed to list udev devices: %s", err)
		}

		klog.Infof("Setting up device %s with devices %s", resourceName, devs)
		dp := NewHostDevicePlugin(devs, socketPath, resourceName, &devconf)

		if err := dp.Start(); err != nil {
			return dps, fmt.Errorf("failed to start device plugin: %s", err)
		}

		if err := dp.Register(pluginapi.KubeletSocket, resourceName); err != nil {
			return dps, fmt.Errorf("failed to register device plugin: %s", err)
		}

		dps[resourceName] = dp
	}

	klog.Info("set up dps ", dps)

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

	klog.Infoln("Starting udev watcher.")
	u := udev.Udev{}
	mon := u.NewMonitorFromNetlink("udev")
	devices, err := mon.DeviceChan(context.Background())
	if err != nil {
		klog.Fatalf("failed to create udev monitor chan: %s\n", err)
	}

	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	defer klog.Info("deferred done")
	restart := false
L:
	for {
		klog.Info("for")
		if restart {
			klog.Info("restart")
			restart = false
			for _, dp := range dps {
				dp.Stop()
			}

			config.load()
			dps, err = createDevicePlugins(config)
			if err != nil {
				klog.Fatalf("failed to create device plugins: %s\n", err)
			}
			klog.Info("restarted")
		}

		klog.Info("selecting")
		select {
		case event := <-kubeletWatcher.Events:
			klog.Info("kubeletwatch event")
			if event.Name == pluginapi.KubeletSocket && event.Op&fsnotify.Create == fsnotify.Create {
				klog.Infof("inotify: %s created, restarting.", pluginapi.KubeletSocket)
				restart = true
			}

		case err := <-kubeletWatcher.Errors:
			klog.Infof("inotify: %s", err)

		case <-ticker.C:
			klog.Info("tick")
			for _, dp := range dps {
				devs, err := dp.deviceConfig.getPluginDevices()
				if err == nil {
					klog.Infof("updated devices to %#v for %s", devs, dp.deviceConfig.ContainerPath)
					dp.Update(devs)
				} else {
					klog.Errorf("failed to get devices for %s: %s", dp.deviceConfig.ContainerPath, err)
				}
			}

		case dev := <-devices:
			klog.Infof("udev update: %v", dev)
			for _, dp := range dps {
				if dp.deviceConfig.matchesProperties(dev) {
					devs, err := dp.deviceConfig.getPluginDevices()
					if err != nil {
						klog.Fatalf("failed to get devices for %s: %s", dp.deviceConfig.ContainerPath, err)
					}
					klog.Infof("updated devices to %#v for %s because of %s event", devs, dp.deviceConfig.ContainerPath, dev.Action())
					dp.Update(devs)
				}
			}

		case s := <-sigWatcher:
			klog.Infof("sigwatcher %s", s)
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
		klog.Info("selected")
	}
	klog.Info("done")
}
