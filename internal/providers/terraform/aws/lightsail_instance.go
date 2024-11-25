package aws

import (
	"github.com/diginfra/diginfra/internal/resources/aws"
	"github.com/diginfra/diginfra/internal/schema"
)

func getLightsailInstanceRegistryItem() *schema.RegistryItem {
	return &schema.RegistryItem{
		Name:      "aws_lightsail_instance",
		CoreRFunc: NewLightsailInstance,
	}
}

func NewLightsailInstance(d *schema.ResourceData) schema.CoreResource {
	r := &aws.LightsailInstance{
		Address:  d.Address,
		BundleID: d.Get("bundle_id").String(),
		Region:   d.Get("region").String(),
	}
	return r
}
