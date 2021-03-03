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
	"bufio"
	"flag"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	appv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog"
)

var (
	pvcClient         corev1.PersistentVolumeClaimInterface
	pvClient          corev1.PersistentVolumeInterface
	podClient         corev1.PodInterface
	deploymentClient  appv1.DeploymentInterface
	statefulSetClient appv1.StatefulSetInterface
	replicaSetClient  appv1.ReplicaSetInterface
)

func main() {
	pvcName := flag.String("pvc", "", "name of pvc")
	namespace := flag.String("ns", "", "namespace of of pvc")
	flag.Parse()

	if len(*pvcName) == 0 || len(*namespace) == 0 {
		klog.Error("pvc and ns are needed, example: ./delete-pvc -ns default -pvc test")
		return
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", filepath.Join(homedir.HomeDir(), ".kube", "config"))
	if err != nil {
		return
	}

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return
	}

	pvcClient = clientSet.CoreV1().PersistentVolumeClaims(*namespace)
	pvClient = clientSet.CoreV1().PersistentVolumes()
	podClient = clientSet.CoreV1().Pods(*namespace)
	deploymentClient = clientSet.AppsV1().Deployments(*namespace)
	statefulSetClient = clientSet.AppsV1().StatefulSets(*namespace)
	replicaSetClient = clientSet.AppsV1().ReplicaSets(*namespace)

	owner, err := workloadOfPVC(*pvcName)
	if err != nil {
		klog.Error("get workload of pvc error:", err)
		return
	}

	if owner == nil {
		klog.Info("pvc not bound by workload ")
		return
	}

	confirm1, err := confirm(fmt.Sprintf("Delete workload %s/%s/%s?", *namespace, owner.Kind, owner.Name))
	if confirm1 {
		err = deleteWorkload(owner)
		if err != nil {
			klog.Error("delete workload of pvc error:", err)
			return
		}

		confirm2, err := confirm(fmt.Sprintf("retain pv of pvc: %s?", *pvcName))
		if confirm2 {
			err = retainPV(*pvcName)
			if err != nil {
				klog.Error("Retain pv of pvc error:", err)
				return
			}
		}

		confirm3, err := confirm(fmt.Sprintf("Delete pvc %s/%s?", *namespace, *pvcName))
		if confirm3 {
			err = pvcClient.Delete(*pvcName, &metav1.DeleteOptions{})
			if err != nil {
				klog.Error("delete pvc error:", err)
				return
			}
		}
	}
}

func workloadOfPVC(pvcName string) (*metav1.OwnerReference, error) {
	podList, err := podClient.List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, pod := range podList.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				podOwner := metav1.GetControllerOf(&pod)
				if podOwner == nil {
					return nil, fmt.Errorf("pod ownerReference is nil")
				}
				switch podOwner.Kind {
				case "StatefulSet":
					return podOwner, nil
				case "ReplicaSet":
					rs, err := replicaSetClient.Get(podOwner.Name, metav1.GetOptions{})
					if err != nil {
						return nil, err
					}
					rsOwner := metav1.GetControllerOf(rs)
					if rsOwner == nil {
						return nil, fmt.Errorf("rs ownerReference is nil")
					}
					return rsOwner, nil
				default:
					return nil, fmt.Errorf("ownerReference kind: %s", podOwner.Kind)
				}
			}
		}
	}
	return nil, nil
}

func deleteWorkload(owner *metav1.OwnerReference) error {
	switch owner.Kind {
	case "StatefulSet":
		return statefulSetClient.Delete(owner.Name, &metav1.DeleteOptions{})
	case "Deployment":
		return deploymentClient.Delete(owner.Name, &metav1.DeleteOptions{})
	default:
		return fmt.Errorf("unkown workload: %s ", owner.Kind)
	}
}

func retainPV(pvcName string) error {
	pvc, err := pvcClient.Get(pvcName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	patch := []byte(`{"spec":{"persistentVolumeReclaimPolicy":"Retain"}}`)
	_, err = pvClient.Patch(pvc.Spec.VolumeName, types.MergePatchType, patch)
	return err
}

func confirm(cue string) (bool, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf(" %s [yes/no]: ", cue)
		input, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}
		input = strings.TrimSpace(input)
		switch input {
		case "yes":
			return true, nil
		case "no":
			return false, nil
		}
	}
}
