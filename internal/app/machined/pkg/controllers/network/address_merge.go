// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package network

import (
	"context"
	"fmt"
	"log"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"

	"github.com/talos-systems/talos/pkg/resources/network"
)

// AddressMergeController merges network.AddressSpec in network.ConfigNamespace and produces final network.AddressSpec in network.Namespace.
type AddressMergeController struct{}

// Name implements controller.Controller interface.
func (ctrl *AddressMergeController) Name() string {
	return "network.AddressMergeController"
}

// Inputs implements controller.Controller interface.
func (ctrl *AddressMergeController) Inputs() []controller.Input {
	return []controller.Input{
		{
			Namespace: network.ConfigNamespaceName,
			Type:      network.AddressSpecType,
			Kind:      controller.InputWeak,
		},
		// TODO: temporary hack to make controller watch its outputs to facilitate proper teardown sequence
		//       should be fixed in the runtime library to automatically support notifications on finalizer change
		//       on outputs
		{
			Namespace: network.NamespaceName,
			Type:      network.AddressSpecType,
			Kind:      controller.InputWeak,
		},
	}
}

// Outputs implements controller.Controller interface.
func (ctrl *AddressMergeController) Outputs() []controller.Output {
	return []controller.Output{
		{
			Type: network.AddressSpecType,
			Kind: controller.OutputShared,
		},
	}
}

// Run implements controller.Controller interface.
//
//nolint: gocyclo
func (ctrl *AddressMergeController) Run(ctx context.Context, r controller.Runtime, logger *log.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		// list source network configuration resources
		list, err := r.List(ctx, resource.NewMetadata(network.ConfigNamespaceName, network.AddressSpecType, "", resource.VersionUndefined))
		if err != nil {
			return fmt.Errorf("error listing source network addresses: %w", err)
		}

		// address is allowed as long as it's not duplicate, for duplicate higher layer takes precedence
		addresses := map[string]*network.AddressSpec{}

		for _, res := range list.Items {
			address := res.(*network.AddressSpec) //nolint:errcheck,forcetypeassert
			id := network.AddressID(address.Status().LinkName, address.Status().Address)

			existing, ok := addresses[id]
			if ok && existing.Status().Layer > address.Status().Layer {
				// skip this address, as existing one is higher layer
				continue
			}

			addresses[id] = address
		}

		for id, address := range addresses {
			address := address

			if err = r.Modify(ctx, network.NewAddressSpec(network.NamespaceName, id), func(res resource.Resource) error {
				addr := res.(*network.AddressSpec) //nolint:errcheck,forcetypeassert

				*addr.Status() = *address.Status()

				return nil
			}); err != nil {
				return fmt.Errorf("error updating resource: %w", err)
			}
		}

		// list addresses for cleanup
		list, err = r.List(ctx, resource.NewMetadata(network.NamespaceName, network.AddressSpecType, "", resource.VersionUndefined))
		if err != nil {
			return fmt.Errorf("error listing resources: %w", err)
		}

		for _, res := range list.Items {
			if _, ok := addresses[res.Metadata().ID()]; !ok {
				var okToDestroy bool

				okToDestroy, err = r.Teardown(ctx, res.Metadata())
				if err != nil {
					return fmt.Errorf("error cleaning up addresses: %w", err)
				}

				if okToDestroy {
					if err = r.Destroy(ctx, res.Metadata()); err != nil {
						return fmt.Errorf("error cleaning up addresses: %w", err)
					}
				}
			}
		}
	}
}
