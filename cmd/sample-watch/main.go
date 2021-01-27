package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/labstack/echo"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/weaveworks/libgitops/cmd/common"
	"github.com/weaveworks/libgitops/cmd/common/logs"
	"github.com/weaveworks/libgitops/cmd/sample-app/apis/sample/scheme"
	"github.com/weaveworks/libgitops/cmd/sample-app/apis/sample/v1alpha1"
	"github.com/weaveworks/libgitops/pkg/serializer"
	"github.com/weaveworks/libgitops/pkg/storage/backend"
	"github.com/weaveworks/libgitops/pkg/storage/client"
	"github.com/weaveworks/libgitops/pkg/storage/core"
	"github.com/weaveworks/libgitops/pkg/storage/event"
	"github.com/weaveworks/libgitops/pkg/storage/filesystem"
	unstructuredevent "github.com/weaveworks/libgitops/pkg/storage/filesystem/unstructured/event"
	"github.com/weaveworks/libgitops/pkg/storage/kube"
)

var watchDirFlag = pflag.String("watch-dir", "/tmp/libgitops/watch", "Where to watch for YAML/JSON manifests")

func main() {
	// Parse the version flag
	common.ParseVersionFlag()

	// Run the application
	if err := run(*watchDirFlag); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(watchDir string) error {
	// Create the watch directory
	if err := os.MkdirAll(*watchDirFlag, 0755); err != nil {
		return err
	}

	// Set the log level
	logs.Logger.SetLevel(logrus.TraceLevel)

	ctx := context.Background()

	rawManifest, err := unstructuredevent.NewManifest(
		watchDir,
		filesystem.DefaultContentTyper,
		core.StaticNamespacer{NamespacedIsDefaultPolicy: false}, // all objects root-spaced
		&core.SerializerObjectRecognizer{Serializer: scheme.Serializer},
		filesystem.DefaultPathExcluders(),
	)
	if err != nil {
		return err
	}

	// Create the channel to receive events to, and register it with the EventStorage
	updates := make(event.ObjectEventStream, 4096)
	if err := rawManifest.WatchForObjectEvents(ctx, updates); err != nil {
		return err
	}

	b, err := backend.NewGeneric(rawManifest, scheme.Serializer, kube.NewNamespaceEnforcer(), nil, nil)
	if err != nil {
		return err
	}

	watchStorage, err := client.NewGeneric(b, scheme.Serializer.Patcher())
	if err != nil {
		return err
	}

	defer func() { _ = rawManifest.Close() }()

	go func() {
		for upd := range updates {
			logrus.Infof("Got %s update for: %v %v", upd.Type, upd.ID.GroupKind(), upd.ID.ObjectKey())
		}
	}()

	e := common.NewEcho()

	e.GET("/watch/:name", func(c echo.Context) error {
		name := c.Param("name")
		if len(name) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "Please set name")
		}

		obj := &v1alpha1.Car{}
		err := watchStorage.Get(ctx, core.ObjectKey{Name: name}, obj)
		if err != nil {
			return err
		}
		var content bytes.Buffer
		if err := scheme.Serializer.Encoder().Encode(serializer.NewJSONFrameWriter(&content), obj); err != nil {
			return err
		}
		return c.JSONBlob(http.StatusOK, content.Bytes())
	})

	e.PUT("/watch/:name", func(c echo.Context) error {
		name := c.Param("name")
		if len(name) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "Please set name")
		}

		if err := common.SetNewCarStatus(ctx, watchStorage, name); err != nil {
			return err
		}
		return c.String(200, "OK!")
	})

	return common.StartEcho(e)
}
