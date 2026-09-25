package providerscheme

import (
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	cpv2alpha1 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"
	providerv1alpha1 "github.com/openmcp-project/openmcp-operator/api/provider/v1alpha1"
	pwv1alpha1 "github.com/openmcp-project/project-workspace-operator/api/core/v1alpha1"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// InstallCRDAPIs installs the CRD APIs in the scheme.
// This is used for the init subcommand.
func InstallCRDAPIs(scheme *runtime.Scheme) *runtime.Scheme {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextv1.AddToScheme(scheme))

	return scheme
}

// InstallOperatorAPIsPlatform builds the scheme for the platform cluster. Our own CRDs
// (NotificationConfig/UserProfile/NotificationRecord) and the ServiceProvider we watch all live
// on the platform cluster.
func InstallOperatorAPIsPlatform(scheme *runtime.Scheme) *runtime.Scheme {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clustersv1alpha1.AddToScheme(scheme))
	utilruntime.Must(providerv1alpha1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	return scheme
}

// InstallOperatorAPIsOnboarding builds the scheme for the onboarding cluster, where the
// membership signals live: Projects/Workspaces and V2 ControlPlanes.
func InstallOperatorAPIsOnboarding(scheme *runtime.Scheme) *runtime.Scheme {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(pwv1alpha1.AddToScheme(scheme))
	utilruntime.Must(cpv2alpha1.AddToScheme(scheme))

	return scheme
}
