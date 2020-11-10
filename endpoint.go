
package devicemanager

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// dial establishes the gRPC communication with the registered device plugin. https://godoc.org/google.golang.org/grpc#Dial
func dial(unixSocketPath string) (pluginapi.DevicePluginClient, *grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := grpc.DialContext(ctx, unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}),
	)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial device plugin: "+" %v", err)
	}

	return pluginapi.NewDevicePluginClient(c), c, nil
}
