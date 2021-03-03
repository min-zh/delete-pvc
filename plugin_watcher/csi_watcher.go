/*
Copyright (C) 2018 Yunify, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this work except in compliance with the License.
You may obtain a copy of the License in the LICENSE file, or at:

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"fmt"
	"gopkg.in/fsnotify.v1"
	"io/ioutil"
	"k8s.io/klog"
	"path/filepath"
	"sync"
	"time"
)

const ignoreDir  = "kubernetes.io"

type csiHandler func(driverName string)

type csiWatcher struct {
	path       string
	fsWatcher  *fsnotify.Watcher
	stopCh     chan interface{}
	wg         sync.WaitGroup
	csiHandler csiHandler
}

func newCSIWatcher(path string, csiHandler csiHandler) *csiWatcher {
	return &csiWatcher{
		path:       path,
		csiHandler: csiHandler,
	}
}

func (w *csiWatcher) start() error {
	w.stopCh = make(chan interface{})
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to start plugin fsWatcher, err: %v", err)
	}
	w.fsWatcher = fsWatcher

	w.wg.Add(1)
	go func(fsWatcher *fsnotify.Watcher) {
		defer w.wg.Done()
		for {
			select {
			case event := <-fsWatcher.Events:
				w.wg.Add(1)
				func() {
					defer w.wg.Done()
					if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Remove == fsnotify.Remove {
						klog.Infof("Handle event: %v", event)
						driverName := filepath.Base(event.Name)
						if driverName != ignoreDir {
							klog.Infof("Handle event: %v, driveName:%s", event, driverName)
							w.csiHandler(driverName)
						}
					} else {
						klog.Infof("Ignore event: %v", event)
					}
					return
				}()
				continue
			case err := <-fsWatcher.Errors:
				if err != nil {
					klog.Errorf("fsWatcher received error: %v", err)
				}
				continue
			case <-w.stopCh:
				return
			}
		}
	}(fsWatcher)
	err = w.init()
	if err != nil {
		return err
	}
	return fsWatcher.Add(w.path)
}

func (w *csiWatcher) init() error {
	files, err := ioutil.ReadDir(w.path)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() && file.Name() != ignoreDir {
			w.wg.Add(1)
			go func(fileName string) {
				defer w.wg.Done()
				w.fsWatcher.Events <- fsnotify.Event{
					Name: fileName,
					Op:   fsnotify.Create,
				}
			}(filepath.Join(w.path, file.Name()))
		}
	}
	return nil

}

func (w *csiWatcher) stop() error {
	close(w.stopCh)
	c := make(chan struct{})
	go func() {
		defer close(c)
		w.wg.Wait()
	}()

	select {
	case <-c:
	case <-time.After(11 * time.Second):
		return fmt.Errorf("timeout on stopping watcher")
	}
	err := w.fsWatcher.Close()
	if err != nil{
		klog.Error("stop error", err)
		return err
	}

	klog.Info("stop OK")
	return nil
}

func main() {
	w := newCSIWatcher("/var/lib/kubelet/plugins", func(driverName string) {
		klog.Info(driverName)
	})
	err := w.start()
	if err != nil {
		klog.Error(err)
	}
	defer func() {
		err = w.stop()
		if err != nil {
			klog.Error(err)
		}
	}()
	time.Sleep(time.Minute)
}
