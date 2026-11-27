package controllers

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *MySQLClusterReconciler) makeV1MySQLDContainer(cluster *mocov1beta2.MySQLCluster) (*corev1ac.ContainerApplyConfiguration, error) {
	var source *corev1ac.ContainerApplyConfiguration

	spec := cluster.Spec.PodTemplate.Spec.DeepCopy()
	for _, c := range spec.Containers {
		if *c.Name == constants.MysqldContainerName {
			source = &c
			break
		}
	}
	if source == nil {
		return nil, fmt.Errorf("MySQLD container not found")
	}

	source.
		WithArgs("--defaults-file="+filepath.Join(constants.MySQLConfPath, constants.MySQLConfName)).
		WithLifecycle(corev1ac.Lifecycle().
			WithPreStop(corev1ac.LifecycleHandler().
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

	failureThreshold := max(cluster.Spec.StartupWaitSeconds/10, 1)

	if source.StartupProbe == nil {
		source.WithStartupProbe(corev1ac.Probe())
	}

	source.StartupProbe.WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath("/healthz").
		WithPort(intstr.FromString(constants.MySQLHealthPortName)).
		WithScheme(corev1.URISchemeHTTP))

	if source.StartupProbe.PeriodSeconds == nil {
		source.StartupProbe.WithPeriodSeconds(10)
	}
	if source.StartupProbe.FailureThreshold == nil {
		source.StartupProbe.WithFailureThreshold(failureThreshold)
	}

	if source.LivenessProbe == nil {
		source.WithLivenessProbe(corev1ac.Probe())
	}

	source.LivenessProbe.WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath("/healthz").
		WithPort(intstr.FromString(constants.MySQLHealthPortName)).
		WithScheme(corev1.URISchemeHTTP))

	if source.ReadinessProbe == nil {
		source.WithReadinessProbe(corev1ac.Probe())
	}

	source.ReadinessProbe.WithHTTPGet(corev1ac.HTTPGetAction().
		WithPath("/readyz").
		WithPort(intstr.FromString(constants.MySQLHealthPortName)).
		WithScheme(corev1.URISchemeHTTP))

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

	r.updateContainerWithSecurityContext(source)

	return source, nil
}

func (r *MySQLClusterReconciler) makeV1AgentContainer(cluster *mocov1beta2.MySQLCluster) *corev1ac.ContainerApplyConfiguration {
	c := corev1ac.Container().
		WithName(constants.AgentContainerName).
		WithImage(r.AgentImage)

	if cluster.Spec.MaxDelaySeconds != nil {
		c.WithArgs("--max-delay", fmt.Sprintf("%ds", *cluster.Spec.MaxDelaySeconds))
	}
	if cluster.Spec.LogRotationSchedule != "" {
		c.WithArgs("--log-rotation-schedule", cluster.Spec.LogRotationSchedule)
	}
	if cluster.Spec.LogRotationSize > 0 {
		c.WithArgs("--log-rotation-size", fmt.Sprintf("%d", cluster.Spec.LogRotationSize))
	}

	if cluster.Spec.AgentUseLocalhost {
		c.WithArgs(constants.MocoMySQLDLocalhostFlag, strconv.FormatBool(cluster.Spec.AgentUseLocalhost))
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
	).WithResources(
		corev1ac.ResourceRequirements().
			WithRequests(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(constants.AgentContainerCPURequest),
				corev1.ResourceMemory: resource.MustParse(constants.AgentContainerMemRequest),
			}).
			WithLimits(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(constants.AgentContainerCPULimit),
				corev1.ResourceMemory: resource.MustParse(constants.AgentContainerMemLimit),
			}),
	)

	r.updateContainerWithSecurityContext(c)
	updateContainerWithOverwriteContainers(cluster, c)

	return c
}

func (r *MySQLClusterReconciler) makeV1SlowQueryLogContainer(cluster *mocov1beta2.MySQLCluster) *corev1ac.ContainerApplyConfiguration {
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
		).
		WithResources(
			corev1ac.ResourceRequirements().
				WithRequests(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.SlowQueryLogAgentCPURequest),
					corev1.ResourceMemory: resource.MustParse(constants.SlowQueryLogAgentMemRequest),
				}).
				WithLimits(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.SlowQueryLogAgentCPULimit),
					corev1.ResourceMemory: resource.MustParse(constants.SlowQueryLogAgentMemLimit),
				}),
		)

	r.updateContainerWithSecurityContext(c)
	updateContainerWithOverwriteContainers(cluster, c)

	return c
}

func (r *MySQLClusterReconciler) makeV1ExporterContainer(cluster *mocov1beta2.MySQLCluster, collectors []string) *corev1ac.ContainerApplyConfiguration {
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
		).
		WithResources(
			corev1ac.ResourceRequirements().
				WithRequests(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.ExporterContainerCPURequest),
					corev1.ResourceMemory: resource.MustParse(constants.ExporterContainerMemRequest),
				}).
				WithLimits(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.ExporterContainerCPULimit),
					corev1.ResourceMemory: resource.MustParse(constants.ExporterContainerMemLimit),
				}),
		)

	for _, cl := range collectors {
		c.WithArgs("--collect." + cl)
	}

	r.updateContainerWithSecurityContext(c)
	updateContainerWithOverwriteContainers(cluster, c)

	return c
}

func (r *MySQLClusterReconciler) makeV1OptionalContainers(cluster *mocov1beta2.MySQLCluster) []*corev1ac.ContainerApplyConfiguration {
	var containers []*corev1ac.ContainerApplyConfiguration

	spec := cluster.Spec.PodTemplate.Spec.DeepCopy()
	for _, c := range spec.Containers {

		if c.Name == nil {
			continue
		}

		r.updateContainerWithSecurityContext(&c)

		switch *c.Name {
		case constants.MysqldContainerName:
		case constants.AgentContainerName:
		case constants.SlowQueryLogAgentContainerName:
			if cluster.Spec.DisableSlowQueryLogContainer {
				containers = append(containers, &c)
			}
		case constants.ExporterContainerName:
			if len(cluster.Spec.Collectors) == 0 {
				containers = append(containers, &c)
			}
		default:
			containers = append(containers, &c)
		}
	}
	return containers
}

func (r *MySQLClusterReconciler) makeV1InitContainer(ctx context.Context, cluster *mocov1beta2.MySQLCluster, image string) ([]*corev1ac.ContainerApplyConfiguration, error) {
	var initContainers []*corev1ac.ContainerApplyConfiguration
	initContainers = append(initContainers, r.makeInitContainerWithCopyMocoInitBin(cluster))

	c, err := r.makeMocoInitContainer(ctx, cluster, image)
	if err != nil {
		return nil, err
	}
	initContainers = append(initContainers, c)

	spec := cluster.Spec.PodTemplate.Spec.DeepCopy()
	for _, given := range spec.InitContainers {
		ic := given
		r.updateContainerWithSecurityContext(&ic)
		initContainers = append(initContainers, &ic)
	}
	return initContainers, nil
}

func (r *MySQLClusterReconciler) makeMocoInitContainer(ctx context.Context, cluster *mocov1beta2.MySQLCluster, image string) (*corev1ac.ContainerApplyConfiguration, error) {
	cmd := []string{
		filepath.Join(constants.SharedPath, constants.InitCommand),
		fmt.Sprintf("%s=%s", constants.MocoInitDataDirFlag, constants.MySQLDataPath),
		fmt.Sprintf("%s=%s", constants.MocoInitConfDirFlag, constants.MySQLInitConfPath),
		fmt.Sprintf("%s=%s", constants.MocoInitTimezoneDataFlag, strconv.FormatBool(cluster.Spec.InitializeTimezoneData)),
		fmt.Sprintf("%d", cluster.Spec.ServerIDBase),
	}

	if cluster.Spec.AgentUseLocalhost {
		cmd = append(cmd, fmt.Sprintf("%s=%t", constants.MocoMySQLDLocalhostFlag, cluster.Spec.AgentUseLocalhost))
	}

	c := corev1ac.Container().
		WithName(constants.InitContainerName).
		WithImage(image).
		WithCommand(cmd...).
		WithEnv(
			corev1ac.EnvVar().
				WithName(constants.PodNameEnvKey).
				WithValueFrom(corev1ac.EnvVarSource().
					WithFieldRef(corev1ac.ObjectFieldSelector().
						WithAPIVersion("v1").
						WithFieldPath("metadata.name")),
				),
		).WithVolumeMounts(
		corev1ac.VolumeMount().
			WithName(constants.TmpVolumeName).
			WithMountPath(constants.TmpPath),
		corev1ac.VolumeMount().
			WithName(constants.MySQLDataVolumeName).
			WithMountPath(constants.MySQLDataPath),
		corev1ac.VolumeMount().
			WithName(constants.MySQLInitConfVolumeName).
			WithMountPath(constants.MySQLInitConfPath),
		corev1ac.VolumeMount().
			WithName(constants.SharedVolumeName).
			WithMountPath(constants.SharedPath),
	).WithResources(
		corev1ac.ResourceRequirements().
			WithRequests(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(constants.InitContainerCPURequest),
				corev1.ResourceMemory: resource.MustParse(constants.InitContainerMemRequest),
			}).
			WithLimits(corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(constants.InitContainerCPULimit),
				corev1.ResourceMemory: resource.MustParse(constants.InitContainerMemLimit),
			}),
	)

	v, ok, err := r.getEnableLowerCaseTableNamesFromConf(ctx, cluster)
	if err != nil {
		return nil, err
	}
	if ok {
		// Flag validation is done in the moco-init container.
		// If invalid, the init container will fail.
		c.WithArgs(fmt.Sprintf("%s=%s", constants.MocoInitLowerCaseTableNamesFlag, v))
	}

	r.updateContainerWithSecurityContext(c)
	updateContainerWithOverwriteContainers(cluster, c)

	return c, nil
}

func (r *MySQLClusterReconciler) makeInitContainerWithCopyMocoInitBin(cluster *mocov1beta2.MySQLCluster) *corev1ac.ContainerApplyConfiguration {
	c := corev1ac.Container().
		WithName(constants.CopyInitContainerName).
		WithImage(r.AgentImage).
		WithCommand("cp",
			filepath.Join("/", constants.InitCommand),
			filepath.Join(constants.SharedPath, constants.InitContainerName)).
		WithResources(
			corev1ac.ResourceRequirements().
				WithRequests(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.InitContainerCPURequest),
					corev1.ResourceMemory: resource.MustParse(constants.InitContainerMemRequest),
				}).
				WithLimits(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.InitContainerCPULimit),
					corev1.ResourceMemory: resource.MustParse(constants.InitContainerMemLimit),
				}),
		).
		WithVolumeMounts(corev1ac.VolumeMount().
			WithName(constants.SharedVolumeName).
			WithMountPath(constants.SharedPath))

	r.updateContainerWithSecurityContext(c)
	updateContainerWithOverwriteContainers(cluster, c)

	return c
}

func (r *MySQLClusterReconciler) getEnableLowerCaseTableNamesFromConf(ctx context.Context, cluster *mocov1beta2.MySQLCluster) (string, bool, error) {
	if cluster.Spec.MySQLConfigMapName == nil {
		return "", false, nil
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: *cluster.Spec.MySQLConfigMapName}, &cm); err != nil {
		return "", false, fmt.Errorf("failed to get user defined mysql conf configmap: %w", err)
	}

	v, ok := cm.Data[constants.LowerCaseTableNamesConfKey]
	return v, ok, nil
}

func (r *MySQLClusterReconciler) updateContainerWithSecurityContext(container *corev1ac.ContainerApplyConfiguration) {
	container.WithSecurityContext(r.applyDefaultContainerSecurityContext(container.SecurityContext))
}

func (r *MySQLClusterReconciler) applyDefaultContainerSecurityContext(sc *corev1ac.SecurityContextApplyConfiguration) *corev1ac.SecurityContextApplyConfiguration {
	if sc == nil {
		sc = corev1ac.SecurityContext()
	}

	if r.DisableDefaultSecurityContext {
		return sc
	}

	if sc.RunAsUser == nil {
		sc.WithRunAsUser(constants.ContainerUID)
	}
	if sc.RunAsGroup == nil {
		sc.WithRunAsGroup(constants.ContainerGID)
	}
	return sc
}

// applyDefaultPodSecurityContext fills unset fields of psc with the defaults applied to
// MOCO-managed pods and returns it. A nil psc is replaced with a new PodSecurityContext.
// FSGroupChangePolicy defaults to OnRootMismatch. FSGroup defaults to constants.ContainerGID
// unless DisableDefaultSecurityContext is enabled, in which case the platform
// (e.g. OpenShift SCC) assigns the fsGroup.
func (r *MySQLClusterReconciler) applyDefaultPodSecurityContext(psc *corev1ac.PodSecurityContextApplyConfiguration) *corev1ac.PodSecurityContextApplyConfiguration {
	if psc == nil {
		psc = corev1ac.PodSecurityContext()
	}
	if !r.DisableDefaultSecurityContext && psc.FSGroup == nil {
		psc.WithFSGroup(constants.ContainerGID)
	}
	if psc.FSGroupChangePolicy == nil {
		psc.WithFSGroupChangePolicy(corev1.FSGroupChangeOnRootMismatch)
	}
	return psc
}

// jobPodSecurityContext returns the PodSecurityContext for backup and restore job Pods.
// The user-specified jc.PodSecurityContext is the base; unset fields are filled with defaults.
func (r *MySQLClusterReconciler) jobPodSecurityContext(jc *mocov1beta2.JobConfig) *corev1ac.PodSecurityContextApplyConfiguration {
	var psc *corev1ac.PodSecurityContextApplyConfiguration
	if jc.PodSecurityContext != nil {
		psc = (*corev1ac.PodSecurityContextApplyConfiguration)(jc.PodSecurityContext.DeepCopy())
	}
	return r.applyDefaultPodSecurityContext(psc)
}

// jobContainerSecurityContext returns the SecurityContext for backup and restore containers.
// The user-specified jc.ContainerSecurityContext is the base; unset fields are filled with defaults.
func (r *MySQLClusterReconciler) jobContainerSecurityContext(jc *mocov1beta2.JobConfig) *corev1ac.SecurityContextApplyConfiguration {
	sc := corev1ac.SecurityContext()
	if jc.ContainerSecurityContext != nil {
		sc = (*corev1ac.SecurityContextApplyConfiguration)(jc.ContainerSecurityContext.DeepCopy())
	}
	// ReadOnlyRootFilesystem is defaulted here rather than in applyDefaultContainerSecurityContext
	// because the latter is also used for MySQL-related containers.
	if sc.ReadOnlyRootFilesystem == nil {
		sc.WithReadOnlyRootFilesystem(true)
	}
	return r.applyDefaultContainerSecurityContext(sc)
}

func updateContainerWithOverwriteContainers(cluster *mocov1beta2.MySQLCluster, container *corev1ac.ContainerApplyConfiguration) {
	if len(cluster.Spec.PodTemplate.OverwriteContainers) == 0 {
		return
	}

	for _, overwrite := range cluster.Spec.PodTemplate.OverwriteContainers {

		if container.Name != nil && *container.Name == overwrite.Name.String() {
			if overwrite.Resources != nil {
				container.WithResources((*corev1ac.ResourceRequirementsApplyConfiguration)(overwrite.Resources))
			}
			if overwrite.SecurityContext != nil {
				container.WithSecurityContext((*corev1ac.SecurityContextApplyConfiguration)(overwrite.SecurityContext))
			}
		}
	}
}
