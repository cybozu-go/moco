package v1beta2

import (
	"encoding/json"
	"unsafe"

	"github.com/cybozu-go/moco/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiconversion "k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

const (
	SpecReplicaServiceTemplateAnnotation = "mysqlcluster.v1beta2.moco.cybozu.com/spec.replicaServiceTemplate"
)

// ConvertTo converts this MySQLCluster to the Hub version (v1beta1).
func (src *MySQLCluster) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1beta1.MySQLCluster)

	return Convert__MySQLCluster_To_v1beta1_MySQLCluster(src, dst, nil)
}

// ConvertFrom converts from the Hub version (v1beta1) to this version.
func (dst *MySQLCluster) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1beta1.MySQLCluster)

	return Convert_v1beta1_MySQLCluster_To__MySQLCluster(src, dst, nil)
}

func Convert__MySQLCluster_To_v1beta1_MySQLCluster(in *MySQLCluster, out *v1beta1.MySQLCluster, s apiconversion.Scope) error {
	if err := autoConvert__MySQLCluster_To_v1beta1_MySQLCluster(in, out, s); err != nil {
		return err
	}

	if err := marshalReplicaServiceTemplate(&in.Spec, out); err != nil {
		return err
	}

	return nil
}

func Convert__MySQLClusterSpec_To_v1beta1_MySQLClusterSpec(in *MySQLClusterSpec, out *v1beta1.MySQLClusterSpec, s apiconversion.Scope) error {
	if err := autoConvert__MySQLClusterSpec_To_v1beta1_MySQLClusterSpec(in, out, s); err != nil {
		return err
	}

	out.ServiceTemplate = (*v1beta1.ServiceTemplate)(unsafe.Pointer(in.PrimaryServiceTemplate))

	return nil
}

func Convert_v1beta1_MySQLCluster_To__MySQLCluster(in *v1beta1.MySQLCluster, out *MySQLCluster, s apiconversion.Scope) error {
	if err := autoConvert_v1beta1_MySQLCluster_To__MySQLCluster(in, out, s); err != nil {
		return err
	}

	if _, err := unmarshalReplicaServiceTemplate(in, out); err != nil {
		return err
	}

	return nil
}

func Convert_v1beta1_MySQLClusterSpec_To__MySQLClusterSpec(in *v1beta1.MySQLClusterSpec, out *MySQLClusterSpec, s apiconversion.Scope) error {
	if err := autoConvert_v1beta1_MySQLClusterSpec_To__MySQLClusterSpec(in, out, s); err != nil {
		return err
	}

	out.PrimaryServiceTemplate = (*ServiceTemplate)(unsafe.Pointer(in.ServiceTemplate))

	return nil
}

// marshalReplicaServiceTemplate stores the service template as json data in the destination object annotations.
func marshalReplicaServiceTemplate(spec *MySQLClusterSpec, dst metav1.Object) error {
	if spec.ReplicaServiceTemplate == nil {
		return nil
	}

	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spec.ReplicaServiceTemplate)
	if err != nil {
		return err
	}

	data, err := json.Marshal(u)
	if err != nil {
		return err
	}

	annotations := dst.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	annotations[SpecReplicaServiceTemplateAnnotation] = string(data)
	dst.SetAnnotations(annotations)

	return nil
}

// unmarshalReplicaServiceTemplate tries to retrieve the data from the annotation and unmarshal it into the service template passed as input.
func unmarshalReplicaServiceTemplate(src metav1.Object, dst *MySQLCluster) (bool, error) {
	data, ok := src.GetAnnotations()[SpecReplicaServiceTemplateAnnotation]
	if !ok {
		return false, nil
	}

	var s *ServiceTemplate

	if err := json.Unmarshal([]byte(data), &s); err != nil {
		return false, err
	}

	dst.Spec.ReplicaServiceTemplate = s

	dstAnnotation := dst.GetAnnotations()

	delete(dstAnnotation, SpecReplicaServiceTemplateAnnotation)

	if len(dstAnnotation) == 0 {
		dst.SetAnnotations(nil)
	}

	return true, nil
}
