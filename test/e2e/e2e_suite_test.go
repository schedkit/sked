/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schedkit/sked/test/utils"
)

// Optional Environment Variables:
// - PROMETHEUS_INSTALL_SKIP=true: Skips Prometheus Operator installation during test setup.
// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
// These variables are useful if Prometheus or CertManager is already installed, avoiding
// re-installation and conflicts.
var (
	skipPrometheusInstall  = os.Getenv("PROMETHEUS_INSTALL_SKIP") == "true"
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"

	isPrometheusOperatorAlreadyInstalled = false
	isCertManagerAlreadyInstalled        = false

	// projectImage is the name of the image which will be built and loaded
	// with the code source changes to be tested.
	projectImage = "example.com/sked:v0.0.1"
)

func setupSuite(t *testing.T) {
	t.Helper()

	_ = utils.UncommentCode("config/default/kustomization.yaml", "#- ../prometheus", "#")

	for _, target := range []string{"generate", "manifests"} {
		cmd := exec.Command("make", target)
		_, err := utils.Run(cmd)
		require.NoError(t, err, "Failed to run make %s", target)
	}

	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err := utils.Run(cmd)
	require.NoError(t, err, "Failed to build the manager(Operator) image")

	require.NoError(t, utils.LoadImageToKindClusterWithName(projectImage),
		"Failed to load the manager(Operator) image into Kind")

	if !skipPrometheusInstall {
		isPrometheusOperatorAlreadyInstalled = utils.IsPrometheusCRDsInstalled()
		if !isPrometheusOperatorAlreadyInstalled {
			t.Log("Installing Prometheus Operator...")
			require.NoError(t, utils.InstallPrometheusOperator(), "Failed to install Prometheus Operator")
		} else {
			t.Log("WARNING: Prometheus Operator is already installed. Skipping installation...")
		}
	}
	if !skipCertManagerInstall {
		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
		if !isCertManagerAlreadyInstalled {
			t.Log("Installing CertManager...")
			require.NoError(t, utils.InstallCertManager(), "Failed to install CertManager")
		} else {
			t.Log("WARNING: CertManager is already installed. Skipping installation...")
		}
	}

	cmd = exec.Command("kubectl", "create", "ns", namespace)
	_, err = utils.Run(cmd)
	require.NoError(t, err, "Failed to create namespace")

	cmd = exec.Command("make", "install")
	_, err = utils.Run(cmd)
	require.NoError(t, err, "Failed to install CRDs")

	cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(cmd)
	require.NoError(t, err, "Failed to deploy the controller-manager")
}

func teardownSuite(t *testing.T) {
	t.Helper()

	cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
	_, _ = utils.Run(cmd)

	cmd = exec.Command("make", "undeploy")
	_, _ = utils.Run(cmd)

	cmd = exec.Command("make", "uninstall")
	_, _ = utils.Run(cmd)

	cmd = exec.Command("kubectl", "delete", "ns", namespace)
	_, _ = utils.Run(cmd)

	if !skipPrometheusInstall && !isPrometheusOperatorAlreadyInstalled {
		t.Log("Uninstalling Prometheus Operator...")
		utils.UninstallPrometheusOperator()
	}
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		t.Log("Uninstalling CertManager...")
		utils.UninstallCertManager()
	}
}
