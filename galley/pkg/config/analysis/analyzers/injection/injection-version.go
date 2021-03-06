// Copyright 2019 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package injection

import (
	"strings"

	v1 "k8s.io/api/core/v1"

	"istio.io/istio/galley/pkg/config/analysis"
	"istio.io/istio/galley/pkg/config/analysis/msg"
	"istio.io/istio/pkg/config/resource"
	"istio.io/istio/pkg/config/schema/collection"
	"istio.io/istio/pkg/config/schema/collections"
)

// VersionAnalyzer checks the version of auto-injection configured with the running proxies on pods.
type VersionAnalyzer struct{}

var _ analysis.Analyzer = &VersionAnalyzer{}

const injectorName = "sidecar-injector-webhook"
const sidecarInjectorName = "sidecarInjectorWebhook"

// podVersion is a helper struct for tracking a resource with its detected
// proxy version.
type podVersion struct {
	Resource     *resource.Instance
	ProxyVersion string
}

// Metadata implements Analyzer.
func (a *VersionAnalyzer) Metadata() analysis.Metadata {
	return analysis.Metadata{
		Name:        "injection.VersionAnalyzer",
		Description: "Checks the version of auto-injection configured with the running proxies on pods",
		Inputs: collection.Names{
			collections.K8SCoreV1Namespaces.Name(),
			collections.K8SCoreV1Pods.Name(),
		},
	}
}

// Analyze implements Analyzer.
func (a *VersionAnalyzer) Analyze(c analysis.Context) {
	injectedNamespaces := make(map[string]struct{})

	// Collect the list of namespaces that have istio injection enabled.
	c.ForEach(collections.K8SCoreV1Namespaces.Name(), func(r *resource.Instance) bool {
		if r.Metadata.Labels[InjectionLabelName] == InjectionLabelEnableValue {
			injectedNamespaces[r.Metadata.FullName.String()] = struct{}{}
		}

		return true
	})

	injectorVersions := make(map[string]struct{})
	var podVersions []podVersion
	c.ForEach(collections.K8SCoreV1Pods.Name(), func(r *resource.Instance) bool {
		pod := r.Message.(*v1.Pod)

		// Check if this is a sidecar injector pod - if it is, note its version.
		if v := tryReturnSidecarInjectorVersion(pod); v != "" {
			injectorVersions[v] = struct{}{}
		}

		if _, ok := injectedNamespaces[pod.GetNamespace()]; !ok {
			return true
		}

		// If the pod has been annotated with a custom sidecar, then ignore as
		// it always overrides the injector logic.
		if r.Metadata.Annotations["sidecar.istio.io/proxyImage"] != "" {
			return true
		}

		for _, container := range pod.Spec.Containers {
			if container.Name != istioProxyName {
				continue
			}
			// Attempt to parse out the version of the proxy.
			v := getContainerNameVersion(&container)
			// We can't check anything without a version; skip the pod.
			if v == "" {
				continue
			}
			// Note the pod/version to check later after we've collected all injector versions.
			podVersions = append(podVersions, podVersion{
				Resource:     r,
				ProxyVersion: v})

		}

		return true
	})

	for iv := range injectorVersions {
		for _, pv := range podVersions {
			if pv.ProxyVersion != iv {
				c.Report(collections.K8SCoreV1Pods.Name(), msg.NewIstioProxyVersionMismatch(pv.Resource, pv.ProxyVersion, iv))
			}
		}
	}
}

// tryReturnSidecarInjectorVersion returns an empty string if the pod is not
// the sidecar injector; otherwise the version of the injector image is
// returned.
func tryReturnSidecarInjectorVersion(p *v1.Pod) string {
	if p.Labels["app"] != sidecarInjectorName {
		return ""
	}

	for _, c := range p.Spec.Containers {
		if c.Name != injectorName {
			continue
		}

		v := getContainerNameVersion(&c)
		return v
	}

	return ""
}

// getContainerNameVersion parses the name and version from a container image.
// If the version is not specified or can't be found, version is the empty
// string.
func getContainerNameVersion(c *v1.Container) (version string) {
	parts := strings.Split(c.Image, ":")
	if len(parts) != 2 {
		return ""
	}
	version = parts[1]
	return
}
