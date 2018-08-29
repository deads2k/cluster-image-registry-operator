package generate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/operator-framework/operator-sdk/pkg/sdk"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	projectapi "github.com/openshift/api/project/v1"

	"github.com/openshift/cluster-image-registry-operator/pkg/apis/dockerregistry/v1alpha1"
	"github.com/openshift/cluster-image-registry-operator/pkg/parameters"
)

func generateLogLevel(cr *v1alpha1.OpenShiftDockerRegistry) string {
	switch cr.Spec.Logging.Level {
	case 0:
		return "error"
	case 1:
		return "warn"
	case 2, 3:
		return "info"
	}
	return "debug"
}

func generateLivenessProbeConfig(p *parameters.Globals) *corev1.Probe {
	probeConfig := generateProbeConfig(p)
	probeConfig.InitialDelaySeconds = 10

	return probeConfig
}

func generateReadinessProbeConfig(p *parameters.Globals) *corev1.Probe {
	return generateProbeConfig(p)
}

func generateProbeConfig(p *parameters.Globals) *corev1.Probe {
	var scheme corev1.URIScheme
	if p.Container.UseTLS {
		scheme = corev1.URISchemeHTTPS
	}
	return &corev1.Probe{
		TimeoutSeconds: int32(p.Healthz.TimeoutSeconds),
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Scheme: scheme,
				Path:   p.Healthz.Route,
				Port:   intstr.FromInt(p.Container.Port),
			},
		},
	}
}

func generateSecurityContext(cr *v1alpha1.OpenShiftDockerRegistry, namespace string) (*corev1.PodSecurityContext, error) {
	ns := &projectapi.Project{
		TypeMeta: metav1.TypeMeta{
			APIVersion: projectapi.SchemeGroupVersion.String(),
			Kind:       "Project",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	err := sdk.Get(ns)
	if err != nil {
		return nil, err
	}

	sgrange, ok := ns.Annotations[parameters.SupplementalGroupsAnnotation]
	if !ok {
		return nil, fmt.Errorf("namespace %q doesn't have annotation %s", namespace, parameters.SupplementalGroupsAnnotation)
	}

	idx := strings.Index(sgrange, "/")
	if idx == -1 {
		return nil, fmt.Errorf("annotation %s in namespace %q doesn't contain '/'", parameters.SupplementalGroupsAnnotation, namespace)
	}

	gid, err := strconv.ParseInt(sgrange[:idx], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unable to parse annotation %s in namespace %q: %s", parameters.SupplementalGroupsAnnotation, namespace, err)
	}

	return &corev1.PodSecurityContext{
		FSGroup: &gid,
	}, nil
}

func PodTemplateSpec(cr *v1alpha1.OpenShiftDockerRegistry, p *parameters.Globals) (corev1.PodTemplateSpec, map[string]string, error) {
	storageType := ""

	var (
		storageConfigured int
		env               []corev1.EnvVar
		mounts            []corev1.VolumeMount
		volumes           []corev1.Volume
	)

	annotations := make(map[string]string)

	env = append(env,
		corev1.EnvVar{Name: "REGISTRY_HTTP_ADDR", Value: fmt.Sprintf(":%d", p.Container.Port)},
		corev1.EnvVar{Name: "REGISTRY_HTTP_NET", Value: "tcp"},
		corev1.EnvVar{Name: "REGISTRY_HTTP_SECRET", Value: cr.Spec.HTTPSecret},
		corev1.EnvVar{Name: "REGISTRY_LOG_LEVEL", Value: generateLogLevel(cr)},
		corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_QUOTA_ENABLED", Value: "true"},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_CACHE_BLOBDESCRIPTOR", Value: "inmemory"},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_DELETE_ENABLED", Value: "true"},
	)

	if cr.Spec.Storage.Filesystem != nil {
		if cr.Spec.Storage.Filesystem.VolumeSource.HostPath != nil {
			return corev1.PodTemplateSpec{}, nil, fmt.Errorf("HostPath is not supported")
		}

		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: "filesystem"},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY", Value: "/registry"},
		)

		vol := corev1.Volume{
			Name:         "registry-storage",
			VolumeSource: cr.Spec.Storage.Filesystem.VolumeSource,
		}

		volumes = append(volumes, vol)
		mounts = append(mounts, corev1.VolumeMount{Name: vol.Name, MountPath: "/registry"})

		storageConfigured += 1
	}

	if cr.Spec.Storage.Azure != nil {
		storageType = "azure"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_AZURE_ACCOUNTNAME", Value: cr.Spec.Storage.Azure.AccountName},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_AZURE_ACCOUNTKEY", Value: cr.Spec.Storage.Azure.AccountKey},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_AZURE_CONTAINER", Value: cr.Spec.Storage.Azure.Container},
		)
		storageConfigured += 1
	}

	if cr.Spec.Storage.GCS != nil {
		storageType = "gcs"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_GCS_BUCKET", Value: cr.Spec.Storage.GCS.Bucket},
		)
		storageConfigured += 1
	}

	if cr.Spec.Storage.S3 != nil {
		storageType = "s3"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_ACCESSKEY", Value: cr.Spec.Storage.S3.AccessKey},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_SECRETKEY", Value: cr.Spec.Storage.S3.SecretKey},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_BUCKET", Value: cr.Spec.Storage.S3.Bucket},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_REGION", Value: cr.Spec.Storage.S3.Region},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_REGIONENDPOINT", Value: cr.Spec.Storage.S3.RegionEndpoint},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_ENCRYPT", Value: fmt.Sprintf("%v", cr.Spec.Storage.S3.Encrypt)},
		)
		storageConfigured += 1
	}

	if cr.Spec.Storage.Swift != nil {
		storageType = "swift"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_AUTHURL", Value: cr.Spec.Storage.Swift.AuthURL},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_USERNAME", Value: cr.Spec.Storage.Swift.Username},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_PASSWORD", Value: cr.Spec.Storage.Swift.Password},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_CONTAINER", Value: cr.Spec.Storage.Swift.Container},
		)
		storageConfigured += 1
	}

	if storageConfigured == 1 {
		annotations[parameters.StorageTypeOperatorAnnotation] = storageType
	} else {
		return corev1.PodTemplateSpec{}, nil, fmt.Errorf("it is not possible to initialize more than one storage backend at the same time")
	}

	if cr.Spec.Requests.Read.MaxRunning != 0 || cr.Spec.Requests.Read.MaxInQueue != 0 {
		if cr.Spec.Requests.Read.MaxRunning < 0 {
			return corev1.PodTemplateSpec{}, nil, fmt.Errorf("Requests.Read.MaxRunning must be positive number")
		}
		if cr.Spec.Requests.Read.MaxInQueue < 0 {
			return corev1.PodTemplateSpec{}, nil, fmt.Errorf("Requests.Read.MaxInQueue must be positive number")
		}
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_REQUESTS_READ_MAXRUNNING", Value: fmt.Sprintf("%d", cr.Spec.Requests.Read.MaxRunning)},
			corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_REQUESTS_READ_MAXINQUEUE", Value: fmt.Sprintf("%d", cr.Spec.Requests.Read.MaxInQueue)},
			corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_REQUESTS_READ_MAXWAITINQUEUE", Value: fmt.Sprintf("%s", cr.Spec.Requests.Read.MaxWaitInQueue)},
		)
	}

	if cr.Spec.Requests.Write.MaxRunning != 0 || cr.Spec.Requests.Write.MaxInQueue != 0 {
		if cr.Spec.Requests.Write.MaxRunning < 0 {
			return corev1.PodTemplateSpec{}, nil, fmt.Errorf("Requests.Write.MaxRunning must be positive number")
		}
		if cr.Spec.Requests.Write.MaxInQueue < 0 {
			return corev1.PodTemplateSpec{}, nil, fmt.Errorf("Requests.Write.MaxInQueue must be positive number")
		}
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_REQUESTS_WRITE_MAXRUNNING", Value: fmt.Sprintf("%d", cr.Spec.Requests.Write.MaxRunning)},
			corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_REQUESTS_WRITE_MAXINQUEUE", Value: fmt.Sprintf("%d", cr.Spec.Requests.Write.MaxInQueue)},
			corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_REQUESTS_WRITE_MAXWAITINQUEUE", Value: fmt.Sprintf("%s", cr.Spec.Requests.Write.MaxWaitInQueue)},
		)
	}

	securityContext, err := generateSecurityContext(cr, p.Deployment.Namespace)
	if err != nil {
		return corev1.PodTemplateSpec{}, nil, fmt.Errorf("generate security context for deployment config: %s", err)
	}

	// Certificates
	vol := corev1.Volume{
		Name: "registry-certificates",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "image-registry-certificates",
				},
			},
		},
	}
	volumes = append(volumes, vol)
	mounts = append(mounts, corev1.VolumeMount{Name: vol.Name, MountPath: "/etc/pki/ca-trust/source/anchors"})

	spec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: p.Deployment.Labels,
		},
		Spec: corev1.PodSpec{
			NodeSelector: cr.Spec.NodeSelector,
			Containers: []corev1.Container{
				{
					Name:  p.Container.Name,
					Image: cr.Spec.ImagePullSpec,
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: int32(p.Container.Port),
							Protocol:      "TCP",
						},
					},
					Env:            env,
					VolumeMounts:   mounts,
					LivenessProbe:  generateLivenessProbeConfig(p),
					ReadinessProbe: generateReadinessProbeConfig(p),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
			Volumes:            volumes,
			ServiceAccountName: p.Pod.ServiceAccount,
			SecurityContext:    securityContext,
		},
	}

	return spec, annotations, nil
}