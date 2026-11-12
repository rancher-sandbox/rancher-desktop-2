// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"encoding/json"
	"fmt"

	mobyvolume "github.com/moby/moby/api/types/volume"

	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

// volumeWhitelistedValidator ensures that only volumes with names listed in the
// static mock data can be created.
type volumeWhiteListValidator struct {
	volumeNames map[string]bool
}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (v *volumeWhiteListValidator) ValidateCreate(_ context.Context, volume *v1alpha1.Volume) (warnings ctrlwebhookadmission.Warnings, err error) {
	if _, ok := v.volumeNames[volume.Name]; !ok {
		return nil, fmt.Errorf("volume %q is not found in the hard-coded list", volume.Name)
	}
	return nil, nil
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (v *volumeWhiteListValidator) ValidateDelete(context.Context, *v1alpha1.Volume) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, nil
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (v *volumeWhiteListValidator) ValidateUpdate(context.Context, *v1alpha1.Volume, *v1alpha1.Volume) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, nil
}

func newVolumeWhiteListValidator(volumeData []byte) (*volumeWhiteListValidator, error) {
	var volumes []mobyvolume.Volume
	if err := json.Unmarshal(volumeData, &volumes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal static volume data: %w", err)
	}
	result := &volumeWhiteListValidator{
		volumeNames: make(map[string]bool),
	}
	for _, vol := range volumes {
		result.volumeNames[sanitizeKubernetesObjectName(vol.Name)] = true
	}
	return result, nil
}
