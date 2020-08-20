package tombstone

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/karlkfi/kubexit/pkg/log"
	"sigs.k8s.io/yaml"
)

type Tombstone struct {
	Born     *time.Time `json:",omitempty"`
	Died     *time.Time `json:",omitempty"`
	ExitCode *int       `json:",omitempty"`

	Graveyard string `json:"-"`
	Name      string `json:"-"`

	fileLock sync.Mutex
}

func (t *Tombstone) Path() string {
	return filepath.Join(t.Graveyard, t.Name)
}

// Write a tombstone file, truncating before writing.
// If the FilePath directories do not exist, they will be created.
func (t *Tombstone) Write() error {
	// one write at a time
	t.fileLock.Lock()
	defer t.fileLock.Unlock()

	err := os.MkdirAll(t.Graveyard, os.ModePerm)
	if err != nil {
		return err
	}

	// does not exit
	file, err := os.Create(t.Path())
	if err != nil {
		return fmt.Errorf("failed to create tombstone file: %v", err)
	}
	defer file.Close()

	pretty, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("failed to marshal tombstone yaml: %v", err)
	}
	file.Write(pretty)
	return nil
}

func (t *Tombstone) RecordBirth(ctx context.Context) error {
	born := time.Now()
	t.Born = &born

	log.G(ctx).Printf("Creating tombstone: %s\n", t.Path())
	err := t.Write()
	if err != nil {
		return fmt.Errorf("failed to create tombstone: %v", err)
	}
	return nil
}

func (t *Tombstone) RecordDeath(ctx context.Context, exitCode int) error {
	code := exitCode
	died := time.Now()
	t.Died = &died
	t.ExitCode = &code

	log.G(ctx).Printf("Updating tombstone: %s\n", t.Path())
	err := t.Write()
	if err != nil {
		return fmt.Errorf("failed to update tombstone: %v", err)
	}
	return nil
}

func (t *Tombstone) String() string {
	inline, err := json.Marshal(t)
	if err != nil {
		log.Printf("Error: failed to marshal tombstone as json: %v\n", err)
		return "{}"
	}
	return string(inline)
}

// Read a tombstone from a graveyard.
func Read(graveyard, name string) (*Tombstone, error) {
	t := Tombstone{
		Graveyard: graveyard,
		Name:      name,
	}

	bytes, err := ioutil.ReadFile(t.Path())
	if err != nil {
		return nil, fmt.Errorf("failed to read tombstone file: %v", err)
	}

	err = yaml.Unmarshal(bytes, &t)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal tombstone yaml: %v", err)
	}

	return &t, nil
}

type EventHandler func(context.Context, fsnotify.Event) error

// LoggingEventHandler is an example EventHandler that logs fsnotify events
func LoggingEventHandler(ctx context.Context, event fsnotify.Event) error {
	if event.Op&fsnotify.Create == fsnotify.Create {
		log.G(ctx).Printf("Tombstone Watch: file created: %s\n", event.Name)
	}
	if event.Op&fsnotify.Remove == fsnotify.Remove {
		log.G(ctx).Printf("Tombstone Watch: file removed: %s\n", event.Name)
	}
	if event.Op&fsnotify.Write == fsnotify.Write {
		log.G(ctx).Printf("Tombstone Watch: file modified: %s\n", event.Name)
	}
	if event.Op&fsnotify.Rename == fsnotify.Rename {
		log.G(ctx).Printf("Tombstone Watch: file renamed: %s\n", event.Name)
	}
	if event.Op&fsnotify.Chmod == fsnotify.Chmod {
		log.G(ctx).Printf("Tombstone Watch: file chmoded: %s\n", event.Name)
	}
	return nil
}

// Watch a graveyard and call the eventHandler (asyncronously) when an
// event happens. When the supplied context is canceled, watching will stop.
func Watch(ctx context.Context, graveyard string, eventHandler EventHandler) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %v", err)
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				log.G(ctx).Printf("Tombstone Watch(%s): done\n", graveyard)
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				err := eventHandler(ctx, event)
				if err != nil {
					log.G(ctx).Printf("Tombstone Watch(%s): error handling file system event: %v\n", graveyard, err)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.G(ctx).Printf("Tombstone Watch(%s): error from fsnotify: %v\n", graveyard, err)
				// TODO: wrap ctx with WithCancel and cancel on terminal errors, if any
			}
		}
	}()

	err = watcher.Add(graveyard)
	if err != nil {
		return fmt.Errorf("failed to add watcher: %v", err)
	}

	files, err := ioutil.ReadDir(graveyard)
	if err != nil {
		return fmt.Errorf("failed to read graveyard dir: %v", err)
	}

	for _, f := range files {
		event := fsnotify.Event{
			Name: filepath.Join(graveyard, f.Name()),
			Op:   fsnotify.Create,
		}
		err = eventHandler(ctx, event)
		if err != nil {
			return fmt.Errorf("failed handling existing tombstone: %v", err)
		}
	}

	return nil
}
