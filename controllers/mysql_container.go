package controllers

import (
	"fmt"
	"path/filepath"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/pointer"
)

func (r *MySQLClusterReconciler) makeV1MySQLDContainer(cluster *mocov1beta2.MySQLCluster, desired, current []corev1ac.ContainerApplyConfiguration) (*corev1ac.ContainerApplyConfiguration, error) {
	var source *corev1ac.ContainerApplyConfiguration
	for i, c := range desired {
		if *c.Name == constants.MysqldContainerName {
			source = &desired[i]
			break
		}
	}
	if source == nil {
		return nil, fmt.Errorf("MySQLD container not found")
	}

	source.
		WithArgs("--defaults-file="+filepath.Join(constants.MySQLConfPath, constants.MySQLConfName)).
		WithLifecycle(corev1ac.Lifecycle().
			WithPreStop(corev1ac.Handler().
				WithExec(corev1ac.ExecAction().
					WithCommand("sleep", constants.PreStopSeconds)),
			),
		).WithPorts(
		corev1ac.ContainerPort().
			WithName(constants.MySQLPortName).
			WithContainerPort(constants.MySQLPort).
			WithProtocol(corev1.ProtocolTCP),
		corev1ac.ContainerPort().
			WithName(constants.MySQLXPortName).WithContainerPort(constants.MySQLXPort).WithProtocol(corev1.ProtocolTCP),
		corev1ac.ContainerPort().
			WithName(constants.MySQLAdminPortName).
			WithContainerPort(constants.MySQLAdminPort).
			WithProtocol(corev1.ProtocolTCP),
		corev1ac.ContainerPort().
			WithName(constants.MySQLHealthPortName).
			WithContainerPort(constants.MySQLHealthPort).
			WithProtocol(corev1.ProtocolTCP),
	)

	failureThreshold := cluster.Spec.StartupWaitSeconds / 10
	if failureThreshold < 1 {
		failureThreshold = 1
	}

	source.
		WithStartupProbe(corev1ac.Probe().
			WithHTTPGet(corev1ac.HTTPGetAction().
				WithPath("/healthz").
				WithPort(intstr.FromString(constants.MySQLHealthPortName)).
				WithScheme(corev1.URISchemeHTTP)).
			WithPeriodSeconds(10).
			WithFailureThreshold(failureThreshold)).
		WithLivenessProbe(corev1ac.Probe().
			WithHTTPGet(corev1ac.HTTPGetAction().
				WithPath("/healthz").
				WithPort(intstr.FromString(constants.MySQLHealthPortName)).
				WithScheme(corev1.URISchemeHTTP))).
		WithReadinessProbe(corev1ac.Probe().
			WithHTTPGet(corev1ac.HTTPGetAction().
				WithPath("/readyz").
				WithPort(intstr.FromString(constants.MySQLHealthPortName)).
				WithScheme(corev1.URISchemeHTTP)),
		)

	source.WithVolumeMounts(
		corev1ac.VolumeMount().
			WithName(constants.TmpVolumeName).
			WithMountPath(constants.TmpPath),
		corev1ac.VolumeMount().
			WithName(constants.RunVolumeName).
			WithMountPath(constants.RunPath),
		corev1ac.VolumeMount().
			WithName(constants.VarLogVolumeName).
			WithMountPath(constants.LogDirPath),
		corev1ac.VolumeMount().
			WithName(constants.MySQLConfVolumeName).
			WithMountPath(constants.MySQLConfPath),
		corev1ac.VolumeMount().
			WithName(constants.MySQLInitConfVolumeName).
			WithMountPath(constants.MySQLInitConfPath),
		corev1ac.VolumeMount().
			WithName(constants.MySQLConfSecretVolumeName).
			WithMountPath(constants.MyCnfSecretPath).
			WithReadOnly(true),
		corev1ac.VolumeMount().
			WithName(constants.MySQLDataVolumeName).
			WithMountPath(constants.MySQLDataPath),
	)

	updateContainerWithSupplementsWithApply(source, current)

	return source, nil
}

func (r *MySQLClusterReconciler) makeV1AgentContainer(cluster *mocov1beta2.MySQLCluster, current []corev1ac.ContainerApplyConfiguration) *corev1ac.ContainerApplyConfiguration {
	c := corev1ac.Container().
		WithName(constants.AgentContainerName).
		WithImage(r.AgentImage)

	if cluster.Spec.MaxDelaySeconds > 0 {
		c.WithArgs("--max-delay", fmt.Sprintf("%ds", cluster.Spec.MaxDelaySeconds))
	}
	if cluster.Spec.LogRotationSchedule != "" {
		c.WithArgs("--log-rotation-schedule", cluster.Spec.LogRotationSchedule)
	}

	c.WithVolumeMounts(
		corev1ac.VolumeMount().
			WithName(constants.RunVolumeName).
			WithMountPath(constants.RunPath),
		corev1ac.VolumeMount().
			WithName(constants.VarLogVolumeName).
			WithMountPath(constants.LogDirPath),
		corev1ac.VolumeMount().
			WithName(constants.GRPCSecretVolumeName).
			WithMountPath("/grpc-cert").
			WithReadOnly(true),
	).WithEnv(
		corev1ac.EnvVar().
			WithName(constants.PodNameEnvKey).
			WithValueFrom(corev1ac.EnvVarSource().
				WithFieldRef(corev1ac.ObjectFieldSelector().
					WithAPIVersion("v1").
					WithFieldPath("metadata.name")),
			),
		corev1ac.EnvVar().
			WithName(constants.ClusterNameEnvKey).
			WithValue(cluster.Name),
	).WithEnvFrom(
		corev1ac.EnvFromSource().
			WithSecretRef(corev1ac.SecretEnvSource().
				WithName(cluster.UserSecretName())),
	).WithPorts(
		corev1ac.ContainerPort().
			WithName(constants.AgentPortName).
			WithContainerPort(constants.AgentPort).
			WithProtocol(corev1.ProtocolTCP),
		corev1ac.ContainerPort().
			WithName(constants.AgentMetricsPortName).
			WithContainerPort(constants.AgentMetricsPort).
			WithProtocol(corev1.ProtocolTCP),
	)

	updateContainerWithSupplementsWithApply(c, current)
	return c
}

func (r *MySQLClusterReconciler) makeV1SlowQueryLogContainer(sts *appsv1ac.StatefulSetApplyConfiguration, force bool) *corev1ac.ContainerApplyConfiguration {
	if !force {
		for _, c := range sts.Spec.Template.Spec.Containers {
			if *c.Name == constants.SlowQueryLogAgentContainerName {
				return &c
			}
		}
	}

	c := corev1ac.Container().
		WithName(constants.SlowQueryLogAgentContainerName).
		WithImage(r.FluentBitImage).
		WithVolumeMounts(
			corev1ac.VolumeMount().
				WithName(constants.SlowQueryLogAgentConfigVolumeName).
				WithMountPath(constants.FluentBitConfigPath).
				WithReadOnly(true),
			corev1ac.VolumeMount().
				WithName(constants.VarLogVolumeName).
				WithMountPath(constants.LogDirPath),
		)

	updateContainerWithSupplementsWithApply(c, sts.Spec.Template.Spec.Containers)
	return c
}

func (r *MySQLClusterReconciler) makeV1ExporterContainer(collectors []string, current []corev1ac.ContainerApplyConfiguration) *corev1ac.ContainerApplyConfiguration {
	c := corev1ac.Container().
		WithName(constants.ExporterContainerName).
		WithImage(r.ExporterImage).
		WithArgs("--config.my-cnf="+filepath.Join(constants.MyCnfSecretPath, constants.ExporterMyCnf)).
		WithPorts(
			corev1ac.ContainerPort().
				WithName(constants.ExporterPortName).
				WithContainerPort(constants.ExporterPort).
				WithProtocol(corev1.ProtocolTCP)).
		WithVolumeMounts(
			corev1ac.VolumeMount().
				WithName(constants.RunVolumeName).
				WithMountPath(constants.RunPath),
			corev1ac.VolumeMount().
				WithName(constants.MySQLConfSecretVolumeName).
				WithMountPath(constants.MyCnfSecretPath).
				WithReadOnly(true),
		)

	for _, cl := range collectors {
		c.WithArgs("--collect." + cl)
	}

	updateContainerWithSupplementsWithApply(c, current)
	return c
}

func (r *MySQLClusterReconciler) makeV1OptionalContainers(cluster *mocov1beta2.MySQLCluster, current []corev1ac.ContainerApplyConfiguration) []*corev1ac.ContainerApplyConfiguration {
	var containers []*corev1ac.ContainerApplyConfiguration
	for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
		c := c
		switch *c.Name {
		case constants.MysqldContainerName:
		case constants.AgentContainerName:
		case constants.SlowQueryLogAgentContainerName:
			if cluster.Spec.DisableSlowQueryLogContainer {
				updateContainerWithSupplementsWithApply(&c, current)
				containers = append(containers, &c)
			}
		case constants.ExporterContainerName:
			if len(cluster.Spec.Collectors) == 0 {
				updateContainerWithSupplementsWithApply(&c, current)
				containers = append(containers, &c)
			}
		default:
			updateContainerWithSupplementsWithApply(&c, current)
			containers = append(containers, &c)
		}
	}
	return containers
}

func (r *MySQLClusterReconciler) makeV1InitContainer(cluster *mocov1beta2.MySQLCluster, image string, current []corev1ac.ContainerApplyConfiguration) []*corev1ac.ContainerApplyConfiguration {
	c := corev1ac.Container().
		WithName(constants.InitContainerName).
		WithImage(image).
		WithCommand(
			constants.InitCommand,
			"--data-dir="+constants.MySQLDataPath,
			"--conf-dir="+constants.MySQLInitConfPath,
			fmt.Sprintf("%d", cluster.Spec.ServerIDBase),
		).WithEnv(
		corev1ac.EnvVar().
			WithName(constants.PodNameEnvKey).
			WithValueFrom(corev1ac.EnvVarSource().
				WithFieldRef(corev1ac.ObjectFieldSelector().
					WithAPIVersion("v1").
					WithFieldPath("metadata.name")),
			),
	).WithVolumeMounts(
		corev1ac.VolumeMount().
			WithName(constants.MySQLDataVolumeName).
			WithMountPath(constants.MySQLDataPath),
		corev1ac.VolumeMount().
			WithName(constants.MySQLInitConfVolumeName).
			WithMountPath(constants.MySQLInitConfPath),
	)

	var initContainers []*corev1ac.ContainerApplyConfiguration
	updateContainerWithSupplementsWithApply(c, current)
	initContainers = append(initContainers, c)
	for _, given := range cluster.Spec.PodTemplate.Spec.InitContainers {
		ic := given
		updateContainerWithSupplementsWithApply(&ic, current)
		initContainers = append(initContainers, &ic)
	}
	return initContainers
}

func updateContainerWithSupplements(container *corev1.Container, currentContainers []corev1.Container) {
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	container.SecurityContext.RunAsUser = pointer.Int64(constants.ContainerUID)
	container.SecurityContext.RunAsGroup = pointer.Int64(constants.ContainerGID)

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

func updateContainerWithSupplementsWithApply(container *corev1ac.ContainerApplyConfiguration, currentContainers []corev1ac.ContainerApplyConfiguration) {
	if container.SecurityContext == nil {
		container.WithSecurityContext(corev1ac.SecurityContext().
			WithRunAsUser(constants.ContainerUID).
			WithRunAsGroup(constants.ContainerGID),
		)
	}

	var current *corev1ac.ContainerApplyConfiguration
	for i, c := range currentContainers {
		if *c.Name == *container.Name {
			current = &currentContainers[i]
			break
		}
	}
	if current == nil {
		return
	}

	if current.ImagePullPolicy == nil {
		container.WithImagePullPolicy(*current.ImagePullPolicy)
	}
	if current.TerminationMessagePath == nil {
		container.WithTerminationMessagePath(*current.TerminationMessagePath)
	}
	if current.TerminationMessagePolicy == nil {
		container.WithTerminationMessagePolicy(*current.TerminationMessagePolicy)
	}
	updateProbeWithSupplementsWithApply(container.StartupProbe, current.StartupProbe)
	updateProbeWithSupplementsWithApply(container.LivenessProbe, current.LivenessProbe)
	updateProbeWithSupplementsWithApply(container.ReadinessProbe, current.ReadinessProbe)
}

func updateProbeWithSupplementsWithApply(probe, current *corev1ac.ProbeApplyConfiguration) {
	if probe == nil || current == nil {
		return
	}

	if probe.FailureThreshold == nil {
		probe.WithFailureThreshold(*current.FailureThreshold)
	}
	if probe.PeriodSeconds == nil {
		probe.WithPeriodSeconds(*current.PeriodSeconds)
	}
	if probe.SuccessThreshold == nil {
		probe.WithSuccessThreshold(*current.SuccessThreshold)
	}
	if probe.TimeoutSeconds == nil {
		probe.WithTimeoutSeconds(*current.TimeoutSeconds)
	}
}
