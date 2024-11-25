package aws

import (
	"github.com/diginfra/diginfra/internal/resources/aws"
	"github.com/diginfra/diginfra/internal/schema"
)

func getNewKMSKeyRegistryItem() *schema.RegistryItem {
	return &schema.RegistryItem{
		Name:      "aws_kms_key",
		CoreRFunc: NewKMSKey,
	}
}

func NewKMSKey(d *schema.ResourceData) schema.CoreResource {
	r := &aws.KMSKey{
		Address:               d.Address,
		Region:                d.Get("region").String(),
		CustomerMasterKeySpec: d.Get("customer_master_key_spec").String(),
	}
	return r
}
