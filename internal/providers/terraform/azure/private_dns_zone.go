package azure

import (
	"github.com/diginfra/diginfra/internal/resources/azure"
	"github.com/diginfra/diginfra/internal/schema"
)

func getDNSPrivateZoneRegistryItem() *schema.RegistryItem {
	return &schema.RegistryItem{
		Name:      "azurerm_private_dns_zone",
		CoreRFunc: NewPrivateDNSZone,
		ReferenceAttributes: []string{
			"resource_group_name",
		},
		Notes: []string{"Most expensive price tier is used."},
	}
}
func NewPrivateDNSZone(d *schema.ResourceData) schema.CoreResource {
	r := &azure.PrivateDNSZone{Address: d.Address, Region: d.Region}
	return r
}
