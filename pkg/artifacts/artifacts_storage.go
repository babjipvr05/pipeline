/*
Copyright 2018 The Knative Authors.

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

package artifacts

import (
	"fmt"

	"github.com/knative/build-pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/knative/build-pipeline/pkg/system"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ArtifactStorageInterface is an interface to define the steps to copy
// an pipeline artifact to/from temporary storage
type ArtifactStorageInterface interface {
	GetCopyToContainerSpec(name, sourcePath, destinationPath string) []corev1.Container
	GetCopyFromContainerSpec(name, sourcePath, destinationPath string) []corev1.Container
	GetSecretsVolumes() []corev1.Volume
	IsPVC() bool
	StorageBasePath(pr *v1alpha1.PipelineRun) string
}

// InitializeArtifactStorage will check if there is there is a
// bucket configured or create a PVC
func InitializeArtifactStorage(pr *v1alpha1.PipelineRun, c kubernetes.Interface) (ArtifactStorageInterface, error) {
	needPVC := false
	configMap, err := c.CoreV1().ConfigMaps(system.Namespace).Get(v1alpha1.BucketConfigName, metav1.GetOptions{})
	if err != nil {
		needPVC = true
	}
	if configMap != nil && configMap.Data != nil {
		if location, ok := configMap.Data[v1alpha1.BucketLocationKey]; !ok {
			if location == "" {
				needPVC = true
			}
		}
	}
	if needPVC {
		err = createPVC(pr, c)
		if err != nil {
			return nil, err
		}
		return &v1alpha1.ArtifactPVC{Name: pr.Name}, nil
	}

	return NewArtifactBucketConfigFromConfigMap(configMap)
}

// GetArtifactStorage returnes the storgae interface to enable
// consumer code to get a container step for copy to/from storage
func GetArtifactStorage(prName string, c kubernetes.Interface) (ArtifactStorageInterface, error) {
	configMap, err := c.CoreV1().ConfigMaps(system.Namespace).Get(v1alpha1.BucketConfigName, metav1.GetOptions{})
	if err != nil {
		return &v1alpha1.ArtifactPVC{Name: prName}, nil
	}
	return NewArtifactBucketConfigFromConfigMap(configMap)
}

// NewArtifactBucketConfigFromConfigMap creates a Bucket from the supplied ConfigMap
func NewArtifactBucketConfigFromConfigMap(configMap *corev1.ConfigMap) (*v1alpha1.ArtifactBucket, error) {
	c := &v1alpha1.ArtifactBucket{}

	if configMap.Data == nil {
		return c, nil
	}
	if location, ok := configMap.Data[v1alpha1.BucketLocationKey]; !ok {
		c.Location = ""
	} else {
		c.Location = location
	}
	sp := v1alpha1.SecretParam{}
	if secretName, ok := configMap.Data[v1alpha1.BucketServiceAccountSecretName]; !ok {
		c.Secrets = nil
	} else {
		if secretKey, ok := configMap.Data[v1alpha1.BucketServiceAccountSecretKey]; !ok {
			c.Secrets = nil
		} else {
			sp.FieldName = "GOOGLE_APPLICATION_CREDENTIALS"
			sp.SecretName = secretName
			sp.SecretKey = secretKey
			c.Secrets = append(c.Secrets, sp)
		}
	}
	return c, nil
}

func createPVC(pr *v1alpha1.PipelineRun, c kubernetes.Interface) error {
	if _, err := c.CoreV1().PersistentVolumeClaims(pr.Namespace).Get(getPVCName(pr), metav1.GetOptions{}); err != nil {
		if errors.IsNotFound(err) {
			pvc := getPVCSpec(pr)
			if _, err := c.CoreV1().PersistentVolumeClaims(pr.Namespace).Create(pvc); err != nil {
				return fmt.Errorf("failed to claim Persistent Volume %q due to error: %s", pr.Name, err)
			}
			return nil
		}
		return fmt.Errorf("failed to get claim Persistent Volume %q due to error: %s", pr.Name, err)
	}
	return nil
}

func getPVCSpec(pr *v1alpha1.PipelineRun) *corev1.PersistentVolumeClaim {
	var pvcSizeBytes int64
	// TODO(shashwathi): make this value configurable
	pvcSizeBytes = 5 * 1024 * 1024 * 1024 // 5 GBs
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       pr.Namespace,
			Name:            getPVCName(pr),
			OwnerReferences: pr.GetOwnerReference(),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			// Multiple tasks should be allowed to read
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.ResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: *resource.NewQuantity(pvcSizeBytes, resource.BinarySI),
				},
			},
		},
	}
}

func getPVCName(pr *v1alpha1.PipelineRun) string {
	return fmt.Sprintf("%s-pvc", pr.Name)
}