/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
//https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/cm/devicemanager/device_plugin_stub.go
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"time"

	"github.com/jochenvg/go-udev"
	"google.golang.org/grpc"

	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// HostDevicePlugin implementation for DevicePlugin.
type HostDevicePlugin struct {
	devs         []*pluginapi.Device
	socket       string
	resourceName string
	// preStartContainerFlag      bool
	// getPreferredAllocationFlag bool

	stop chan interface{}
	// wg     sync.WaitGroup
	update chan []*pluginapi.Device

	server *grpc.Server

	// allocFunc is used for handling allocation request
	deviceConfig *DeviceConfig

	// registrationStatus chan watcherapi.RegistrationStatus // for testing
	// endpoint           string                             // for testing

}

// NewHostDevicePlugin returns an initialized DevicePlugin Stub.
func NewHostDevicePlugin(devs []*pluginapi.Device, socket string, name string, config *DeviceConfig) *HostDevicePlugin {
	return &HostDevicePlugin{
		devs:         devs,
		socket:       socket,
		resourceName: name,
		// preStartContainerFlag:      preStartContainerFlag,
		// getPreferredAllocationFlag: getPreferredAllocationFlag,

		stop:   make(chan interface{}),
		update: make(chan []*pluginapi.Device),

		deviceConfig: config,
	}
}

// dial establishes the gRPC communication with the registered device plugin.
func dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	c, err := grpc.Dial(unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithTimeout(timeout),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		return nil, err
	}

	return c, nil
}

// Start starts the gRPC server of the device plugin. Can only
// be called once.
func (m *HostDevicePlugin) Start() error {
	err := m.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", m.socket)
	if err != nil {
		return err
	}

	m.server = grpc.NewServer([]grpc.ServerOption{}...)
	pluginapi.RegisterDevicePluginServer(m.server, m)

	go m.server.Serve(sock)
	klog.Infof("Starting to serve on %v", m.socket)

	// Wait for server to start by launching a blocking connexion
	conn, err := dial(m.socket, 60*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// Stop stops the gRPC server. Can be called without a prior Start
// and more than once. Not safe to be called concurrently by different
// goroutines!
func (m *HostDevicePlugin) Stop() error {
	if m.server == nil {
		return nil
	}
	m.server.Stop()
	m.server = nil
	close(m.stop) // This prevents re-starting the server.

	return m.cleanup()
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *HostDevicePlugin) Register(kubeletEndpoint, resourceName string) error {
	conn, err := dial(kubeletEndpoint, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(m.socket),
		ResourceName: resourceName,
	}

	_, err = client.Register(context.Background(), reqt)
	if err != nil {
		return err
	}
	return nil
}

func (m *HostDevicePlugin) GetDevicePluginOptions(ctx context.Context, e *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

func (m *HostDevicePlugin) PreStartContainer(ctx context.Context, r *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (m *HostDevicePlugin) GetPreferredAllocation(ctx context.Context, r *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

// ListAndWatch lists devices and update that list according to the Update call
func (m *HostDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	klog.Info("ListAndWatch")

	s.Send(&pluginapi.ListAndWatchResponse{Devices: m.devs})

	for {
		select {
		case <-m.stop:
			return nil
		case updated := <-m.update:
			s.Send(&pluginapi.ListAndWatchResponse{Devices: updated})
		}
	}
}

// Update allows the device plugin to send new devices through ListAndWatch
func (m *HostDevicePlugin) Update(devs []*pluginapi.Device) {
	m.update <- devs
}

// Allocate does a mock allocation
func (m *HostDevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	klog.Infof("Allocate, %+v", r)

	devs := make(map[string]pluginapi.Device)

	for _, dev := range m.devs {
		devs[dev.ID] = *dev
	}

	var u udev.Udev
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

			if m.deviceConfig.Permissions == "" {
				return nil, fmt.Errorf("permissions for device cannot be empty for device %s", requestID)
			}

			ud := u.NewDeviceFromSyspath(dev.ID)
			hp, _ := ud.DevlinkIterator().Next()
			response.Devices = append(response.Devices, &pluginapi.DeviceSpec{
				ContainerPath: m.deviceConfig.ContainerPath,
				HostPath:      hp.(string),
				Permissions:   m.deviceConfig.Permissions,
			})
		}
		responses.ContainerResponses = append(responses.ContainerResponses, response)
	}

	return &responses, nil
}

func (m *HostDevicePlugin) cleanup() error {
	if err := os.Remove(m.socket); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}