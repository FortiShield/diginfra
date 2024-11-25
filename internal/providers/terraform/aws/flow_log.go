package aws

import (
	"github.com/diginfra/diginfra/internal/schema"
)

func getFlowLogRegistryItem() *schema.RegistryItem {
	return &schema.RegistryItem{
		Name: "aws_flow_log",
		CoreRFunc: func(d *schema.ResourceData) schema.CoreResource {
			return schema.BlankCoreResource{
				Name: d.Address,
				Type: d.Type,
			}
		},
	}
}
