//go:build e2e_mailhog

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

const (
	mailhogNamespace = "openmcp-system"
	mailhogName      = "mailhog"
	mailhogSMTPPort  = 1025
	mailhogHTTPPort  = 8025
)

// mailhogMessages is the MailHog /api/v2/messages response envelope.
type mailhogMessages struct {
	Total int `json:"total"`
}

// TestMailHog deploys MailHog into the landscape, wires NotificationConfig to it, and asserts
// that a welcome email is delivered when a UserProfile is created.
//
// To keep the landscape alive after the test for manual exploration, set KEEP=1:
//
//	KEEP=1 task test-e2e-mailhog
//
// While parked the kind cluster stays up; press Ctrl-C to tear it down.
func TestMailHog(t *testing.T) {
	feature := features.New("mailhog delivery").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			// Register standard k8s types (Deployment, Service …) with the resources client.
			if err := k8sscheme.AddToScheme(c.Client().Resources().GetScheme()); err != nil {
				t.Fatalf("register k8s scheme: %v", err)
			}
			// Register our own types.
			_ = v1alpha1.AddToScheme(c.Client().Resources().GetScheme())

			// 1 — Deploy MailHog.
			if err := c.Client().Resources().Create(ctx, mailhogDeployment()); err != nil {
				t.Fatalf("create mailhog deployment: %v", err)
			}
			if err := c.Client().Resources().Create(ctx, mailhogService()); err != nil {
				t.Fatalf("create mailhog service: %v", err)
			}

			// 2 — Create the NotificationConfig pointing at MailHog.
			//     No SecretRef: MailHog accepts all mail without auth.
			falseBool := false
			cfg := &v1alpha1.NotificationConfig{}
			cfg.SetName("notifications")
			cfg.Spec.ProductName = "Test Platform"
			cfg.Spec.WebAppURL = "https://console.example.test"
			cfg.Spec.Email = &v1alpha1.EmailConfig{
				Host:          mailhogName + "." + mailhogNamespace + ".svc",
				Port:          mailhogSMTPPort,
				StartTLS:      &falseBool,
				SenderAddress: "no-reply@notifications.test",
				SenderName:    "Test Notifications",
			}
			if err := c.Client().Resources().Create(ctx, cfg); err != nil {
				t.Fatalf("create NotificationConfig: %v", err)
			}
			return ctx
		}).
		Assess("MailHog deployment is available", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if err := wait.For(
				conditions.New(c.Client().Resources()).DeploymentAvailable(mailhogName, mailhogNamespace),
				wait.WithTimeout(3*time.Minute),
			); err != nil {
				t.Fatalf("MailHog not available after 3 min: %v", err)
			}
			return ctx
		}).
		Assess("NotificationConfig becomes Ready", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			cfg := &v1alpha1.NotificationConfig{}
			cfg.SetName("notifications")
			if err := wait.For(
				openmcpconditions.Match(cfg, c, "Ready", corev1.ConditionTrue),
				wait.WithTimeout(2*time.Minute),
			); err != nil {
				t.Fatalf("NotificationConfig not Ready after 2 min: %v", err)
			}
			return ctx
		}).
		Assess("welcome email delivered to MailHog", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			// Creating a UserProfile triggers the EnablementReconciler, which sends the
			// one-time welcome email via the MailHog SMTP relay.
			profile := &v1alpha1.UserProfile{}
			profile.SetName("up-mailhogtest")
			profile.Spec.Subject = v1alpha1.Subject{
				Kind: v1alpha1.SubjectKindUser,
				Name: "mailhog-demo@example.com",
			}
			if err := c.Client().Resources().Create(ctx, profile); err != nil {
				t.Fatalf("create UserProfile: %v", err)
			}

			// Query MailHog's REST API through the kube-apiserver service proxy
			// (no port-forward needed from the test process).
			cs, err := kubernetes.NewForConfig(c.Client().RESTConfig())
			if err != nil {
				t.Fatalf("build kubernetes clientset: %v", err)
			}
			if err := wait.For(func(ctx context.Context) (bool, error) {
				raw, proxyErr := cs.CoreV1().Services(mailhogNamespace).
					ProxyGet("http", mailhogName, fmt.Sprintf("%d", mailhogHTTPPort), "/api/v2/messages", nil).
					DoRaw(ctx)
				if proxyErr != nil {
					return false, nil // not yet reachable; retry
				}
				var msgs mailhogMessages
				if jsonErr := json.Unmarshal(raw, &msgs); jsonErr != nil {
					return false, nil
				}
				return msgs.Total > 0, nil
			}, wait.WithTimeout(2*time.Minute)); err != nil {
				t.Fatalf("no email in MailHog within 2 min: %v", err)
			}
			t.Log("✓ Welcome email delivered to MailHog")
			return ctx
		}).
		Assess("explore (KEEP=1 parks here)", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if os.Getenv("KEEP") == "" {
				return ctx
			}
			t.Logf("\n\n══════════════════════════════════════════════════════════")
			t.Logf("Landscape is parked. Kind cluster: %s", platformClusterName)
			t.Logf("")
			t.Logf("To open the MailHog UI on http://localhost:%d:", mailhogHTTPPort)
			t.Logf("  kind get kubeconfig --name %s > /tmp/kind-notifications.yaml", platformClusterName)
			t.Logf("  kubectl --kubeconfig /tmp/kind-notifications.yaml \\")
			t.Logf("    port-forward -n %s svc/%s %d:%d", mailhogNamespace, mailhogName, mailhogHTTPPort, mailhogHTTPPort)
			t.Logf("")
			t.Logf("Press Ctrl-C to tear down the landscape.")
			t.Logf("══════════════════════════════════════════════════════════\n")

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh
			signal.Stop(sigCh)
			t.Log("Signal received — proceeding to teardown.")
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			// Best-effort cleanup. The harness destroys the whole cluster on Finish anyway.
			_ = c.Client().Resources().Delete(ctx, &v1alpha1.UserProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "up-mailhogtest"},
			})
			notifCfg := &v1alpha1.NotificationConfig{}
			notifCfg.SetName("notifications")
			_ = resources.DeleteObject(ctx, c, notifCfg)
			_ = c.Client().Resources().Delete(ctx, mailhogService())
			_ = c.Client().Resources().Delete(ctx, mailhogDeployment())
			return ctx
		})

	testenv.Test(t, feature.Feature())
}

// mailhogDeployment returns the MailHog Deployment manifest.
func mailhogDeployment() *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mailhogName,
			Namespace: mailhogNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": mailhogName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": mailhogName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  mailhogName,
						Image: "mailhog/mailhog:latest",
						Ports: []corev1.ContainerPort{
							{Name: "smtp", ContainerPort: mailhogSMTPPort, Protocol: corev1.ProtocolTCP},
							{Name: "http", ContainerPort: mailhogHTTPPort, Protocol: corev1.ProtocolTCP},
						},
					}},
				},
			},
		},
	}
}

// mailhogService returns the MailHog Service manifest.
func mailhogService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mailhogName,
			Namespace: mailhogNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": mailhogName},
			Ports: []corev1.ServicePort{
				{
					Name:       "smtp",
					Port:       mailhogSMTPPort,
					TargetPort: intstr.FromInt32(mailhogSMTPPort),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "http",
					Port:       mailhogHTTPPort,
					TargetPort: intstr.FromInt32(mailhogHTTPPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}
