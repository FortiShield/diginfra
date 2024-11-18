package azure

import (
	"strings"

	"github.com/diginfra/diginfra/internal/resources/azure"
	"github.com/diginfra/diginfra/internal/schema"
)

func getVirtualNetworkPeeringRegistryItem() *schema.RegistryItem {
	return &schema.RegistryItem{
		Name:      "azurerm_virtual_network_peering",
		CoreRFunc: newVirtualNetworkPeering,
		ReferenceAttributes: []string{
			"virtual_network_name",
			"remote_virtual_network_id",
			"resource_group_name",
		},
		GetRegion: func(defaultRegion string, d *schema.ResourceData) string {
			return lookupRegion(d, []string{"virtual_network_name"})
		},
	}
}

func newVirtualNetworkPeering(d *schema.ResourceData) schema.CoreResource {
	sourceRegion := d.Region
	destinationRegion := lookupRegion(d, []string{"remote_virtual_network_id"})

	sourceZone := virtualNetworkPeeringConvertRegion(sourceRegion)
	destinationZone := virtualNetworkPeeringConvertRegion(destinationRegion)

	r := &azure.VirtualNetworkPeering{
		Address:           d.Address,
		DestinationRegion: destinationRegion,
		SourceRegion:      sourceRegion,
		DestinationZone:   destinationZone,
		SourceZone:        sourceZone,
	}
	return r
}

func virtualNetworkPeeringConvertRegion(region string) string {
	zone := regionToVNETZone(region)

	if strings.HasPrefix(strings.ToLower(region), "china") {
		zone = "CN Zone 1"
	}

	return zone
}