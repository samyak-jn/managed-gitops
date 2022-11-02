package core

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sharedutil "github.com/redhat-appstudio/managed-gitops/backend-shared/util"
	"github.com/redhat-appstudio/managed-gitops/tests-e2e/fixture"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.DebugLevel)))
})

func TestCore(t *testing.T) {
	_, reporterConfig := GinkgoConfiguration()
	// A test is "slow" if it takes longer than a few minutes
	reporterConfig.SlowSpecThreshold = time.Duration(6 * time.Minute)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Core Suite", reporterConfig)
}

func EnsureCleanSlate() error {

	if !sharedutil.IsKCPVirtualWorkspaceDisabled() {
		Expect(fixture.EnsureCleanSlateNonKCPVirtualWorkspace()).To(Succeed())
	} else {
		Expect(fixture.EnsureCleanSlateKCPVirtualWorkspace()).To(Succeed())
	}

	return fmt.Errorf("Error in deciding which function to use against EnsureCleanSlate")
}
