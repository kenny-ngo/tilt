package engine

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/windmilleng/tilt/internal/dockercompose"
	"github.com/windmilleng/tilt/internal/logger"
	"github.com/windmilleng/tilt/internal/model"
	"github.com/windmilleng/tilt/internal/store"
)

// Collects logs from running docker-compose services.
type DockerComposeLogManager struct {
	watches map[model.ManifestName]dockerComposeLogWatch
	dcc     dockercompose.DockerComposeClient
}

func NewDockerComposeLogManager(dcc dockercompose.DockerComposeClient) *DockerComposeLogManager {
	return &DockerComposeLogManager{
		watches: make(map[model.ManifestName]dockerComposeLogWatch),
		dcc:     dcc,
	}
}

// Diff the current watches against set of current docker-compose services, i.e.
// what we SHOULD be watching, returning the changes we need to make.
func (m *DockerComposeLogManager) diff(ctx context.Context, st store.RStore) (setup []dockerComposeLogWatch, teardown []dockerComposeLogWatch) {
	state := st.RLockState()
	defer st.RUnlockState()

	// If we're not watching the mounts, then don't bother watching logs.
	if !state.WatchMounts {
		return nil, nil
	}

	for _, ms := range state.ManifestStates {
		if !ms.Manifest.IsDC() {
			continue
		}
		dcInfo := ms.Manifest.DCInfo()

		existing, isActive := m.watches[ms.Manifest.Name]
		startWatchTime := time.Unix(0, 0)
		if isActive {
			if existing.ctx.Err() == nil {
				// Watcher is still active, no action needed.
				continue
			}

			// The active log watcher got cancelled somehow, so we need to create
			// a new one that picks up where it left off.
			startWatchTime = <-existing.terminationTime
		}

		ctx, cancel := context.WithCancel(ctx)
		w := dockerComposeLogWatch{
			ctx:             ctx,
			cancel:          cancel,
			name:            ms.Manifest.Name,
			dcConfigPath:    dcInfo.ConfigPath,
			startWatchTime:  startWatchTime,
			terminationTime: make(chan time.Time, 1),
		}
		m.watches[ms.Manifest.Name] = w
		setup = append(setup, w)
	}

	for key, value := range m.watches {
		_, inState := state.ManifestStates[key]
		if !inState {
			delete(m.watches, key)

			teardown = append(teardown, value)
		}
	}

	return setup, teardown
}

func (m *DockerComposeLogManager) OnChange(ctx context.Context, st store.RStore) {
	setup, teardown := m.diff(ctx, st)
	for _, watch := range teardown {
		watch.cancel()
	}

	for _, watch := range setup {
		go m.consumeLogs(watch, st)
	}
}

func (m *DockerComposeLogManager) consumeLogs(watch dockerComposeLogWatch, st store.RStore) {
	defer func() {
		watch.terminationTime <- time.Now()
	}()

	name := watch.name
	readCloser, err := m.dcc.StreamLogs(watch.ctx, watch.dcConfigPath, watch.name.String())
	if err != nil {
		logger.Get(watch.ctx).Infof("Error streaming %s logs: %v", name, err)
		return
	}
	defer func() {
		_ = readCloser.Close()
	}()

	// TODO(maia): docker-compose already prefixes logs, but maybe we want to roll
	// our own (as in PodWatchManager) cuz it's prettier?
	globalLogWriter := DockerComposeGlobalLogWriter{
		writer: logger.Get(watch.ctx).Writer(logger.InfoLvl),
	}
	actionWriter := DockerComposeLogActionWriter{
		store:        st,
		manifestName: name,
	}
	multiWriter := io.MultiWriter(globalLogWriter, actionWriter)

	_, err = io.Copy(multiWriter, NewHardCancelReader(watch.ctx, readCloser))
	if err != nil && watch.ctx.Err() == nil {
		logger.Get(watch.ctx).Infof("Error streaming %s logs: %v", name, err)
		return
	}
}

type dockerComposeLogWatch struct {
	ctx             context.Context
	cancel          func()
	name            model.ManifestName
	dcConfigPath    string
	startWatchTime  time.Time
	terminationTime chan time.Time

	// TODO(maia): do we need to track these? (maybe if we implement with `docker logs <cID>`...)
	// cID             container.ID
	// cName           container.Name
}

type DockerComposeLogActionWriter struct {
	store        store.RStore
	manifestName model.ManifestName
}

func (w DockerComposeLogActionWriter) Write(p []byte) (n int, err error) {
	if shouldFilterDCLog(p) {
		return len(p), nil
	}
	w.store.Dispatch(DockerComposeLogAction{
		ManifestName: w.manifestName,
		Log:          append([]byte{}, p...),
	})
	return len(p), nil
}

var _ store.Subscriber = &DockerComposeLogManager{}

func shouldFilterDCLog(p []byte) bool {
	if bytes.HasPrefix(p, []byte("Attaching to ")) {
		return true
	}

	return false
}

type DockerComposeGlobalLogWriter struct {
	writer io.Writer
}

func (w DockerComposeGlobalLogWriter) Write(p []byte) (n int, err error) {
	if shouldFilterDCLog(p) {
		return len(p), nil
	}

	return w.writer.Write(p)
}
