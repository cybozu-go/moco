package controllers

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"text/template"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

var decUnstructured = yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)

var certificateObj = &unstructured.Unstructured{}

func init() {
	certificateObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
}

//go:embed certificate_tmpl.yaml
var certTmplData string

var certTmpl = template.Must(template.New("").Parse(certTmplData))

type certTmplVal struct {
	Name            string
	Namespace       string
	ServiceName     string
	TargetNamespace string
}

func (r *MySQLClusterReconciler) reconcileV1Certificate(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	obj := certificateObj.DeepCopy()
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.CertificateName()}, obj)
	if err == nil {
		return nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get certificate %s: %w", cluster.CertificateName(), err)
	}

	buf := new(bytes.Buffer)
	err = certTmpl.Execute(buf, certTmplVal{
		Name:            cluster.CertificateName(),
		Namespace:       r.SystemNamespace,
		ServiceName:     cluster.HeadlessServiceName(),
		TargetNamespace: cluster.Namespace,
	})
	if err != nil {
		return err
	}
	obj = &unstructured.Unstructured{}
	_, _, err = decUnstructured.Decode(buf.Bytes(), nil, obj)
	if err != nil {
		return fmt.Errorf("failed to decode certificate YAML: %w", err)
	}
	obj.SetLabels(labelSet(cluster, true))

	if err := r.Client.Create(ctx, obj); err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}
	return nil
}

func (r *MySQLClusterReconciler) reconcileV1GRPCSecret(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	controllerSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.CertificateName()}, controllerSecret)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	secret := &corev1.Secret{}
	secret.Namespace = cluster.Namespace
	secret.Name = cluster.GRPCSecretName()
	result, err := ctrl.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Labels = mergeMap(secret.Labels, labelSet(cluster, false))
		secret.Data = controllerSecret.Data
		return ctrl.SetControllerReference(cluster, secret, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile gRPC secret: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled gRPC secret", "operation", string(result))
	}

	return nil
}
