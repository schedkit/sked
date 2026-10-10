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

package rbac

// +kubebuilder:rbac:groups="",namespace=system,roleName=leader-election-role,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,namespace=system,roleName=leader-election-role,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",namespace=system,roleName=leader-election-role,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=authentication.k8s.io,roleName=metrics-auth-role,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,roleName=metrics-auth-role,resources=subjectaccessreviews,verbs=create
// +kubebuilder:rbac:urls=/metrics,roleName=metrics-reader,verbs=get
