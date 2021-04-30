package controllers

import (
	"fmt"
	"path/filepath"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (r *MySQLClusterReconciler) makeV1MySQLDContainer(desired, current []corev1.Container) (corev1.Container, error) {
	var source *corev1.Container
	for i, c := range desired {
		if c.Name == constants.MysqldContainerName {
			source = &desired[i]
			break
		}
	}
	if source == nil {
		return corev1.Container{}, fmt.Errorf("MySQLD container not found")
	}

	c := source.DeepCopy()
	c.Args = []string{"--defaults-file=" + filepath.Join(constants.MySQLConfPath, constants.MySQLConfName)}
	c.Lifecycle = &corev1.Lifecycle{
		PreStop: &corev1.Handler{
			Exec: &corev1.ExecAction{Command: []string{"sleep", constants.PreStopSeconds}},
		},
	}
	c.Ports = append(c.Ports,
		corev1.ContainerPort{ContainerPort: constants.MySQLPort, Name: constants.MySQLPortName, Protocol: corev1.ProtocolTCP},
		corev1.ContainerPort{ContainerPort: constants.MySQLXPort, Name: constants.MySQLXPortName, Protocol: corev1.ProtocolTCP},
		corev1.ContainerPort{ContainerPort: constants.MySQLAdminPort, Name: constants.MySQLAdminPortName, Protocol: corev1.ProtocolTCP},
		corev1.ContainerPort{ContainerPort: constants.MySQLHealthPort, Name: constants.MySQLHealthPortName, Protocol: corev1.ProtocolTCP},
	)
	c.StartupProbe = &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/healthz",
				Port:   intstr.FromString(constants.MySQLHealthPortName),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		PeriodSeconds:    10,
		FailureThreshold: 360, // tolerate up to 1 hour of startup time
	}
	c.LivenessProbe = &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/healthz",
				Port:   intstr.FromString(constants.MySQLHealthPortName),
				Scheme: corev1.URISchemeHTTP,
			},
		},
	}
	c.ReadinessProbe = &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/readyz",
				Port:   intstr.FromString(constants.MySQLHealthPortName),
				Scheme: corev1.URISchemeHTTP,
			},
		},
	}
	c.VolumeMounts = append(c.VolumeMounts,
		corev1.VolumeMount{
			MountPath: constants.TmpPath,
			Name:      constants.TmpVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.RunPath,
			Name:      constants.RunVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.LogDirPath,
			Name:      constants.VarLogVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLConfPath,
			Name:      constants.MySQLConfVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLInitConfPath,
			Name:      constants.MySQLInitConfVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MyCnfSecretPath,
			Name:      constants.MySQLConfSecretVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLDataPath,
			Name:      constants.MySQLDataVolumeName,
		},
	)

	updateContainerWithSupplements(c, current)
	return *c, nil
}

func (r *MySQLClusterReconciler) makeV1AgentContainer(cluster *mocov1beta1.MySQLCluster, current []corev1.Container) corev1.Container {
	c := corev1.Container{}
	c.Name = constants.AgentContainerName
	c.Image = r.AgentContainerImage
	if cluster.Spec.MaxDelaySeconds > 0 {
		c.Args = append(c.Args, "--max-delay", fmt.Sprintf("%ds", cluster.Spec.MaxDelaySeconds))
	}
	if cluster.Spec.LogRotationSchedule != "" {
		c.Args = append(c.Args, "--log-rotation-schedule", cluster.Spec.LogRotationSchedule)
	}
	c.VolumeMounts = []corev1.VolumeMount{
		{
			MountPath: constants.RunPath,
			Name:      constants.RunVolumeName,
		},
		{
			MountPath: constants.LogDirPath,
			Name:      constants.VarLogVolumeName,
		},
	}
	c.Env = []corev1.EnvVar{
		{
			Name: constants.PodNameEnvKey,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "metadata.name",
				},
			},
		},
		{
			Name:  constants.ClusterNameEnvKey,
			Value: cluster.Name,
		},
	}
	c.EnvFrom = []corev1.EnvFromSource{
		{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: cluster.UserSecretName(),
				},
			},
		},
	}
	c.Ports = []corev1.ContainerPort{
		{ContainerPort: constants.AgentPort, Name: constants.AgentPortName, Protocol: corev1.ProtocolTCP},
		{ContainerPort: constants.AgentMetricsPort, Name: constants.AgentMetricsPortName, Protocol: corev1.ProtocolTCP},
	}

	updateContainerWithSupplements(&c, current)
	return c
}

func (r *MySQLClusterReconciler) makeV1SlowQueryLogContainer(sts *appsv1.StatefulSet, force bool) corev1.Container {
	if !force {
		for _, c := range sts.Spec.Template.Spec.Containers {
			if c.Name == constants.SlowQueryLogAgentContainerName {
				return c
			}
		}
	}

	return corev1.Container{
		Name:  constants.SlowQueryLogAgentContainerName,
		Image: r.FluentBitImage,
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: constants.FluentBitConfigPath,
				Name:      constants.SlowQueryLogAgentConfigVolumeName,
				ReadOnly:  true,
			},
			{
				MountPath: constants.LogDirPath,
				Name:      constants.VarLogVolumeName,
			},
		},
	}
}

func (r *MySQLClusterReconciler) makeV1OptionalContainers(cluster *mocov1beta1.MySQLCluster, current []corev1.Container) []corev1.Container {
	var containers []corev1.Container
	for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
		switch c.Name {
		case constants.MysqldContainerName:
		case constants.AgentContainerName:
		case constants.SlowQueryLogAgentContainerName:
			if cluster.Spec.DisableSlowQueryLogContainer {
				cp := c.DeepCopy()
				updateContainerWithSupplements(cp, current)
				containers = append(containers, *cp)
			}
		default:
			cp := c.DeepCopy()
			updateContainerWithSupplements(cp, current)
			containers = append(containers, *cp)
		}
	}
	return containers
}

func (r *MySQLClusterReconciler) makeV1InitContainer(cluster *mocov1beta1.MySQLCluster, image string, current []corev1.Container) []corev1.Container {
	c := corev1.Container{
		Name:  constants.InitContainerName,
		Image: image,
		Command: []string{
			constants.InitCommand,
			"--data-dir=" + constants.MySQLDataPath,
			"--conf-dir=" + constants.MySQLInitConfPath,
			fmt.Sprintf("%d", cluster.Spec.ServerIDBase),
		},
		Env: []corev1.EnvVar{
			{
				Name: constants.PodNameEnvKey,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "metadata.name",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: constants.MySQLDataPath,
				Name:      constants.MySQLDataVolumeName,
			},
			{
				MountPath: constants.MySQLInitConfPath,
				Name:      constants.MySQLInitConfVolumeName,
			},
		},
	}

	var initContainers []corev1.Container
	updateContainerWithSupplements(&c, current)
	initContainers = append(initContainers, c)
	for _, given := range cluster.Spec.PodTemplate.Spec.InitContainers {
		ic := given.DeepCopy()
		updateContainerWithSupplements(ic, current)
		initContainers = append(initContainers, *ic)
	}
	return initContainers
}

func updateContainerWithSupplements(container *corev1.Container, currentContainers []corev1.Container) {
	var current *corev1.Container
	for i, c := range currentContainers {
		if c.Name == container.Name {
			current = &currentContainers[i]
			break
		}
	}
	if current == nil {
		return
	}

	if len(current.ImagePullPolicy) > 0 {
		container.ImagePullPolicy = current.ImagePullPolicy
	}
	if len(current.TerminationMessagePath) > 0 {
		container.TerminationMessagePath = current.TerminationMessagePath
	}
	if len(current.TerminationMessagePolicy) > 0 {
		container.TerminationMessagePolicy = current.TerminationMessagePolicy
	}
	updateProbeWithSupplements(container.StartupProbe, current.StartupProbe)
	updateProbeWithSupplements(container.LivenessProbe, current.LivenessProbe)
	updateProbeWithSupplements(container.ReadinessProbe, current.ReadinessProbe)
}

func updateProbeWithSupplements(probe, current *corev1.Probe) {
	if probe == nil || current == nil {
		return
	}

	if probe.FailureThreshold == 0 {
		probe.FailureThreshold = current.FailureThreshold
	}
	if probe.PeriodSeconds == 0 {
		probe.PeriodSeconds = current.PeriodSeconds
	}
	if probe.SuccessThreshold == 0 {
		probe.SuccessThreshold = current.SuccessThreshold
	}
	if probe.TimeoutSeconds == 0 {
		probe.TimeoutSeconds = current.TimeoutSeconds
	}
}
