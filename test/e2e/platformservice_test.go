package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// TestPlatformService is a smoke test: it creates the singleton NotificationConfig on the
// platform cluster and asserts that the provider's ConfigReconciler applies it and reports Ready.
// The full membership/enablement/digest delivery flow (against a MailHog SMTP sidecar) is a
// separate, heavier e2e scenario.
func TestPlatformService(t *testing.T) {
	basicPlatformServiceTest := features.New("notifications config test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			_ = v1alpha1.AddToScheme(c.Client().Resources().GetScheme())
			config := &v1alpha1.NotificationConfig{}
			config.SetName("notifications")
			// No Email block: the config applies without SMTP and should reach Ready.
			config.Spec.WebAppURL = "https://console.example.test"
			if err := c.Client().Resources().Create(ctx, config); err != nil {
				t.Errorf("failed to create NotificationConfig object: %v", err)
			}
			return ctx
		}).
		Assess("NotificationConfig becomes Ready",
			func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
				_ = v1alpha1.AddToScheme(c.Client().Resources().GetScheme())
				config := &v1alpha1.NotificationConfig{}
				config.SetName("notifications")
				if err := wait.For(openmcpconditions.Match(config, c, "Ready", corev1.ConditionTrue)); err != nil {
					t.Error(err)
				}
				return ctx
			}).
		Assess("NotificationConfig can be deleted",
			func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
				config := &v1alpha1.NotificationConfig{}
				config.SetName("notifications")
				if err := resources.DeleteObject(ctx, c, config, wait.WithTimeout(time.Minute)); err != nil {
					t.Errorf("failed to delete NotificationConfig object: %v", err)
				}
				return ctx
			})
	testenv.Test(t, basicPlatformServiceTest.Feature())
}
