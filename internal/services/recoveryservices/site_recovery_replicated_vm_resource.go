package recoveryservices

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/recoveryservices/mgmt/2018-07-10/siterecovery" // nolint: staticcheck
	"github.com/hashicorp/go-azure-helpers/lang/response"
	"github.com/hashicorp/go-azure-helpers/resourcemanager/commonschema"
	"github.com/hashicorp/go-azure-sdk/resource-manager/compute/2022-03-02/disks"
	"github.com/hashicorp/go-azure-sdk/resource-manager/recoveryservicessiterecovery/2022-10-01/replicationprotecteditems"
	"github.com/hashicorp/terraform-provider-azurerm/helpers/azure"
	"github.com/hashicorp/terraform-provider-azurerm/helpers/tf"
	"github.com/hashicorp/terraform-provider-azurerm/internal/clients"
	"github.com/hashicorp/terraform-provider-azurerm/internal/features"
	keyVaultValidate "github.com/hashicorp/terraform-provider-azurerm/internal/services/keyvault/validate"
	"github.com/hashicorp/terraform-provider-azurerm/internal/services/recoveryservices/parse"
	"github.com/hashicorp/terraform-provider-azurerm/internal/services/recoveryservices/validate"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/pluginsdk"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/suppress"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/validation"
	"github.com/hashicorp/terraform-provider-azurerm/internal/timeouts"
	"github.com/hashicorp/terraform-provider-azurerm/utils"
)

func resourceSiteRecoveryReplicatedVM() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Create: resourceSiteRecoveryReplicatedItemCreate,
		Read:   resourceSiteRecoveryReplicatedItemRead,
		Update: resourceSiteRecoveryReplicatedItemUpdate,
		Delete: resourceSiteRecoveryReplicatedItemDelete,
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := parse.ReplicationProtectedItemID(id)
			return err
		}),

		Timeouts: &pluginsdk.ResourceTimeout{
			Create: pluginsdk.DefaultTimeout(180 * time.Minute),
			Read:   pluginsdk.DefaultTimeout(5 * time.Minute),
			Update: pluginsdk.DefaultTimeout(80 * time.Minute),
			Delete: pluginsdk.DefaultTimeout(80 * time.Minute),
		},

		Schema: map[string]*pluginsdk.Schema{
			"name": {
				Type:         pluginsdk.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},

			"resource_group_name": commonschema.ResourceGroupName(),

			"recovery_vault_name": {
				Type:         pluginsdk.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.RecoveryServicesVaultName,
			},
			"source_recovery_fabric_name": {
				Type:         pluginsdk.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			"source_vm_id": {
				Type:             pluginsdk.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateFunc:     azure.ValidateResourceID,
				DiffSuppressFunc: suppress.CaseDifference,
			},

			"target_recovery_fabric_id": {
				Type:             pluginsdk.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateFunc:     azure.ValidateResourceID,
				DiffSuppressFunc: suppress.CaseDifference,
			},

			"recovery_replication_policy_id": {
				Type:             pluginsdk.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateFunc:     azure.ValidateResourceID,
				DiffSuppressFunc: suppress.CaseDifference,
			},

			"source_recovery_protection_container_name": {
				Type:         pluginsdk.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},

			"target_recovery_protection_container_id": {
				Type:             pluginsdk.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateFunc:     azure.ValidateResourceID,
				DiffSuppressFunc: suppress.CaseDifference,
			},

			"target_resource_group_id": {
				Type:             pluginsdk.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateFunc:     azure.ValidateResourceID,
				DiffSuppressFunc: suppress.CaseDifference,
			},

			"target_availability_set_id": {
				Type:             pluginsdk.TypeString,
				Optional:         true,
				ValidateFunc:     azure.ValidateResourceID,
				DiffSuppressFunc: suppress.CaseDifference,
				ConflictsWith: []string{
					"target_zone",
				},
			},

			"target_zone": commonschema.ZoneSingleOptionalForceNew(),

			"target_network_id": {
				Type:         pluginsdk.TypeString,
				Computed:     true,
				Optional:     true,
				ValidateFunc: azure.ValidateResourceID,
			},

			"managed_disk": {
				Type:       pluginsdk.TypeSet,
				ConfigMode: pluginsdk.SchemaConfigModeAttr,
				Optional:   true,
				ForceNew:   true,
				Set:        resourceSiteRecoveryReplicatedVMDiskHash,
				Elem: &pluginsdk.Resource{
					Schema: map[string]*pluginsdk.Schema{
						"disk_id": {
							Type:             pluginsdk.TypeString,
							Required:         true,
							ForceNew:         true,
							ValidateFunc:     validation.StringIsNotEmpty,
							DiffSuppressFunc: suppress.CaseDifference,
						},
						"staging_storage_account_id": {
							Type:             pluginsdk.TypeString,
							Required:         true,
							ForceNew:         true,
							ValidateFunc:     azure.ValidateResourceID,
							DiffSuppressFunc: suppress.CaseDifference,
						},
						"target_resource_group_id": {
							Type:             pluginsdk.TypeString,
							Required:         true,
							ForceNew:         true,
							ValidateFunc:     azure.ValidateResourceID,
							DiffSuppressFunc: suppress.CaseDifference,
						},
						"target_disk_type": {
							Type:     pluginsdk.TypeString,
							Required: true,
							ForceNew: true,
							ValidateFunc: validation.StringInSlice([]string{
								string(disks.DiskStorageAccountTypesStandardLRS),
								string(disks.DiskStorageAccountTypesPremiumLRS),
								string(disks.DiskStorageAccountTypesStandardSSDLRS),
								string(disks.DiskStorageAccountTypesUltraSSDLRS),
							}, false),
						},
						"target_replica_disk_type": {
							Type:     pluginsdk.TypeString,
							Required: true,
							ForceNew: true,
							ValidateFunc: validation.StringInSlice([]string{
								string(disks.DiskStorageAccountTypesStandardLRS),
								string(disks.DiskStorageAccountTypesPremiumLRS),
								string(disks.DiskStorageAccountTypesStandardSSDLRS),
								string(disks.DiskStorageAccountTypesUltraSSDLRS),
							}, false),
						},
						"target_disk_encryption_set_id": {
							Type:             pluginsdk.TypeString,
							Optional:         true,
							ForceNew:         true,
							ValidateFunc:     azure.ValidateResourceID,
							DiffSuppressFunc: suppress.CaseDifference,
						},

						"target_disk_encryption": {
							Type:       pluginsdk.TypeList,
							ConfigMode: pluginsdk.SchemaConfigModeAttr,
							Optional:   true,
							MaxItems:   1,
							Elem:       diskEncryptionResource(),
						},
					},
				},
			},
			"network_interface": {
				Type:       pluginsdk.TypeSet,
				ConfigMode: pluginsdk.SchemaConfigModeAttr,
				Computed:   true,
				Optional:   true,
				Elem:       networkInterfaceResource(),
			},
		},
	}
}

func networkInterfaceResource() *pluginsdk.Resource {
	out := &pluginsdk.Resource{
		Schema: map[string]*pluginsdk.Schema{
			"source_network_interface_id": {
				Type:         pluginsdk.TypeString,
				Computed:     true,
				Optional:     true,
				ValidateFunc: azure.ValidateResourceID,
			},
			"target_static_ip": {
				Type:         pluginsdk.TypeString,
				Optional:     true,
				ForceNew:     false,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			"target_subnet_name": {
				Type:         pluginsdk.TypeString,
				Optional:     true,
				ForceNew:     false,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			"recovery_public_ip_address_id": {
				Type:         pluginsdk.TypeString,
				Optional:     true,
				ForceNew:     false,
				ValidateFunc: azure.ValidateResourceID,
			},
		},
	}

	if !features.FourPointOhBeta() {
		out.Schema["is_primary"] = &pluginsdk.Schema{
			Deprecated: "this property is not used and will be removed in version 4.0 of the provider",
			Type:       pluginsdk.TypeBool,
			Optional:   true,
			Default:    false,
		}
	}
	return out
}

func diskEncryptionResource() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Schema: map[string]*pluginsdk.Schema{
			"disk_encryption_key": {
				Type:       pluginsdk.TypeList,
				ConfigMode: pluginsdk.SchemaConfigModeAttr,
				Required:   true,
				MaxItems:   1,
				Elem: &pluginsdk.Resource{
					Schema: map[string]*pluginsdk.Schema{
						"secret_url": {
							Type:         pluginsdk.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: keyVaultValidate.NestedItemId,
						},

						"vault_id": {
							Type:         pluginsdk.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: keyVaultValidate.VaultID,
						},
					},
				},
			},
			"key_encryption_key": {
				Type:       pluginsdk.TypeList,
				ConfigMode: pluginsdk.SchemaConfigModeAttr,
				Optional:   true,
				MaxItems:   1,
				Elem: &pluginsdk.Resource{
					Schema: map[string]*pluginsdk.Schema{
						"key_url": {
							Type:         pluginsdk.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: keyVaultValidate.NestedItemId,
						},

						"vault_id": {
							Type:         pluginsdk.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: keyVaultValidate.VaultID,
						},
					},
				},
			},
		},
	}
}

func resourceSiteRecoveryReplicatedItemCreate(d *pluginsdk.ResourceData, meta interface{}) error {
	subscriptionId := meta.(*clients.Client).Account.SubscriptionId
	resGroup := d.Get("resource_group_name").(string)
	vaultName := d.Get("recovery_vault_name").(string)
	client := meta.(*clients.Client).RecoveryServices.ReplicationProtectedItemsClient

	name := d.Get("name").(string)
	fabricName := d.Get("source_recovery_fabric_name").(string)
	sourceVmId := d.Get("source_vm_id").(string)
	policyId := d.Get("recovery_replication_policy_id").(string)
	sourceProtectionContainerName := d.Get("source_recovery_protection_container_name").(string)
	targetProtectionContainerId := d.Get("target_recovery_protection_container_id").(string)
	targetResourceGroupId := d.Get("target_resource_group_id").(string)

	var targetAvailabilitySetID *string
	if id, isSet := d.GetOk("target_availability_set_id"); isSet {
		targetAvailabilitySetID = utils.String(id.(string))
	} else {
		targetAvailabilitySetID = nil
	}

	var targetAvailabilityZone *string
	if zone, isSet := d.GetOk("target_zone"); isSet {
		targetAvailabilityZone = utils.String(zone.(string))
	} else {
		targetAvailabilityZone = nil
	}

	ctx, cancel := timeouts.ForCreate(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id := replicationprotecteditems.NewReplicationProtectedItemID(subscriptionId, resGroup, vaultName, fabricName, sourceProtectionContainerName, name)

	if d.IsNewResource() {
		existing, err := client.Get(ctx, id)
		if err != nil {
			if !response.WasNotFound(existing.HttpResponse) {
				return fmt.Errorf("checking for presence of existing site recovery replicated vm %s (vault %s): %+v", name, vaultName, err)
			}
		}

		if existing.Model != nil && existing.Model.Id != nil && *existing.Model.Id != "" {
			return tf.ImportAsExistsError("azurerm_site_recovery_replicated_vm", *existing.Model.Id)
		}
	}

	var managedDisks []replicationprotecteditems.A2AVMManagedDiskInputDetails

	for _, raw := range d.Get("managed_disk").(*pluginsdk.Set).List() {
		diskInput := raw.(map[string]interface{})
		diskId := diskInput["disk_id"].(string)
		primaryStagingAzureStorageAccountID := diskInput["staging_storage_account_id"].(string)
		recoveryResourceGroupId := diskInput["target_resource_group_id"].(string)
		targetReplicaDiskType := diskInput["target_replica_disk_type"].(string)
		targetDiskType := diskInput["target_disk_type"].(string)
		targetEncryptionDiskSetID := diskInput["target_disk_encryption_set_id"].(string)

		managedDisks = append(managedDisks, replicationprotecteditems.A2AVMManagedDiskInputDetails{
			DiskId:                              diskId,
			PrimaryStagingAzureStorageAccountId: primaryStagingAzureStorageAccountID,
			RecoveryResourceGroupId:             recoveryResourceGroupId,
			RecoveryReplicaDiskAccountType:      &targetReplicaDiskType,
			RecoveryTargetDiskAccountType:       &targetDiskType,
			RecoveryDiskEncryptionSetId:         &targetEncryptionDiskSetID,
			DiskEncryptionInfo:                  expandDiskEncryption(diskInput["target_disk_encryption"].([]interface{})),
		})
	}

	parameters := replicationprotecteditems.EnableProtectionInput{
		Properties: &replicationprotecteditems.EnableProtectionInputProperties{
			PolicyId: &policyId,
			ProviderSpecificDetails: replicationprotecteditems.A2AEnableProtectionInput{
				FabricObjectId:            sourceVmId,
				RecoveryContainerId:       &targetProtectionContainerId,
				RecoveryResourceGroupId:   &targetResourceGroupId,
				RecoveryAvailabilitySetId: targetAvailabilitySetID,
				RecoveryAvailabilityZone:  targetAvailabilityZone,
				VMManagedDisks:            &managedDisks,
			},
		},
	}
	err := client.CreateThenPoll(ctx, id, parameters)
	if err != nil {
		return fmt.Errorf("creating replicated vm %s (vault %s): %+v", name, vaultName, err)
	}

	d.SetId(id.ID())

	// We are not allowed to configure the NIC on the initial setup, and the VM has to be replicated before
	// we can reconfigure. Hence this call to update when we create.
	return resourceSiteRecoveryReplicatedItemUpdateInternal(ctx, d, meta)
}

func resourceSiteRecoveryReplicatedItemUpdate(d *pluginsdk.ResourceData, meta interface{}) error {
	ctx, cancel := timeouts.ForUpdate(meta.(*clients.Client).StopContext, d)
	defer cancel()
	return resourceSiteRecoveryReplicatedItemUpdateInternal(ctx, d, meta)
}

func resourceSiteRecoveryReplicatedItemUpdateInternal(ctx context.Context, d *pluginsdk.ResourceData, meta interface{}) error {
	subscriptionId := meta.(*clients.Client).Account.SubscriptionId
	resGroup := d.Get("resource_group_name").(string)
	vaultName := d.Get("recovery_vault_name").(string)
	client := meta.(*clients.Client).RecoveryServices.ReplicationProtectedItemsClient

	// We are only allowed to update the configuration once the VM is fully protected
	state, err := waitForReplicationToBeHealthy(ctx, d, meta)
	if err != nil {
		return err
	}

	name := d.Get("name").(string)
	fabricName := d.Get("source_recovery_fabric_name").(string)
	sourceProtectionContainerName := d.Get("source_recovery_protection_container_name").(string)
	targetNetworkId := d.Get("target_network_id").(string)

	id := replicationprotecteditems.NewReplicationProtectedItemID(subscriptionId, resGroup, vaultName, fabricName, sourceProtectionContainerName, name)

	var targetAvailabilitySetID *string
	if id, isSet := d.GetOk("target_availability_set_id"); isSet {
		tmp := id.(string)
		targetAvailabilitySetID = &tmp
	} else {
		targetAvailabilitySetID = nil
	}

	var vmNics []replicationprotecteditems.VMNicInputDetails
	nicList := d.Get("network_interface").(*pluginsdk.Set).List()
	for _, raw := range nicList {
		vmNicInput := raw.(map[string]interface{})
		sourceNicId := vmNicInput["source_network_interface_id"].(string)
		targetStaticIp := vmNicInput["target_static_ip"].(string)
		targetSubnetName := vmNicInput["target_subnet_name"].(string)
		recoveryPublicIPAddressID := vmNicInput["recovery_public_ip_address_id"].(string)

		nicId := findNicId(state, sourceNicId)
		if nicId == nil {
			return fmt.Errorf("updating replicated vm %s (vault %s): Trying to update NIC that is not known by Azure %s", name, vaultName, sourceNicId)
		}
		ipConfig := []replicationprotecteditems.IPConfigInputDetails{
			{
				RecoverySubnetName:        &targetSubnetName,
				RecoveryStaticIPAddress:   &targetStaticIp,
				RecoveryPublicIPAddressId: &recoveryPublicIPAddressID,
				IsPrimary:                 utils.Bool(true), // currently we can only set one IPconfig for a nic, so we dont need to expose this to users.
			},
		}
		vmNics = append(vmNics, replicationprotecteditems.VMNicInputDetails{
			NicId:     nicId,
			IPConfigs: &ipConfig,
		})
	}

	var managedDisks []replicationprotecteditems.A2AVMManagedDiskUpdateDetails
	for _, raw := range d.Get("managed_disk").(*pluginsdk.Set).List() {
		diskInput := raw.(map[string]interface{})
		diskId := diskInput["disk_id"].(string)
		targetReplicaDiskType := diskInput["target_replica_disk_type"].(string)
		targetDiskType := diskInput["target_disk_type"].(string)

		managedDisks = append(managedDisks, replicationprotecteditems.A2AVMManagedDiskUpdateDetails{
			DiskId:                         &diskId,
			RecoveryReplicaDiskAccountType: &targetReplicaDiskType,
			RecoveryTargetDiskAccountType:  &targetDiskType,
			DiskEncryptionInfo:             expandDiskEncryption(diskInput["target_disk_encryption"].([]interface{})),
		})
	}

	if targetNetworkId == "" {
		// No target network id was specified, so we want to preserve what was selected
		if a2aDetails, isA2a := state.Properties.ProviderSpecificDetails.(replicationprotecteditems.A2AReplicationDetails); isA2a {
			if a2aDetails.SelectedRecoveryAzureNetworkId != nil {
				targetNetworkId = *a2aDetails.SelectedRecoveryAzureNetworkId
			} else {
				return fmt.Errorf("target_network_id must be set when a network_interface is configured")
			}
		} else {
			return fmt.Errorf("target_network_id must be set when a network_interface is configured")
		}
	}

	parameters := replicationprotecteditems.UpdateReplicationProtectedItemInput{
		Properties: &replicationprotecteditems.UpdateReplicationProtectedItemInputProperties{
			RecoveryAzureVMName:            &name,
			SelectedRecoveryAzureNetworkId: &targetNetworkId,
			VMNics:                         &vmNics,
			RecoveryAvailabilitySetId:      targetAvailabilitySetID,
			ProviderSpecificDetails: replicationprotecteditems.A2AUpdateReplicationProtectedItemInput{
				ManagedDiskUpdateDetails: &managedDisks,
			},
		},
	}

	err = client.UpdateThenPoll(ctx, id, parameters)
	if err != nil {
		return fmt.Errorf("updating replicated vm %s (vault %s): %+v", name, vaultName, err)
	}

	return resourceSiteRecoveryReplicatedItemRead(d, meta)
}

func findNicId(state *replicationprotecteditems.ReplicationProtectedItem, sourceNicId string) *string {
	if a2aDetails, isA2a := state.Properties.ProviderSpecificDetails.(replicationprotecteditems.A2AReplicationDetails); isA2a {
		if a2aDetails.VMNics != nil {
			for _, nic := range *a2aDetails.VMNics {
				if nic.SourceNicArmId != nil && *nic.SourceNicArmId == sourceNicId {
					return nic.NicId
				}
			}
		}
	}
	return nil
}

func resourceSiteRecoveryReplicatedItemRead(d *pluginsdk.ResourceData, meta interface{}) error {
	id, err := replicationprotecteditems.ParseReplicationProtectedItemID(d.Id())
	if err != nil {
		return err
	}

	client := meta.(*clients.Client).RecoveryServices.ReplicationProtectedItemsClient

	ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
	defer cancel()

	resp, err := client.Get(ctx, *id)
	if err != nil {
		if response.WasNotFound(resp.HttpResponse) {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("making Read request on site recovery replicated vm %s: %+v", id.String(), err)
	}

	model := resp.Model
	if model == nil {
		return fmt.Errorf("making Read request on site recovery replicated vm %s: model is nil", id.String())
	}

	d.Set("name", id.ReplicatedProtectedItemName)
	d.Set("resource_group_name", id.ResourceGroupName)
	d.Set("recovery_vault_name", id.ResourceName)
	d.Set("source_recovery_fabric_name", id.FabricName)
	d.Set("source_recovery_protection_container_name", id.ProtectionContainerName)

	if prop := model.Properties; prop != nil {
		d.Set("target_recovery_fabric_id", prop.RecoveryFabricId)
		d.Set("recovery_replication_policy_id", prop.PolicyId)
		d.Set("target_recovery_protection_container_id", prop.RecoveryContainerId)

		if a2aDetails, isA2a := prop.ProviderSpecificDetails.(replicationprotecteditems.A2AReplicationDetails); isA2a {
			d.Set("source_vm_id", a2aDetails.FabricObjectId)
			d.Set("target_resource_group_id", a2aDetails.RecoveryAzureResourceGroupId)
			d.Set("target_availability_set_id", a2aDetails.RecoveryAvailabilitySet)
			d.Set("target_zone", a2aDetails.RecoveryAvailabilityZone)
			d.Set("target_network_id", a2aDetails.SelectedRecoveryAzureNetworkId)

			if a2aDetails.ProtectedManagedDisks != nil {
				disksOutput := make([]interface{}, 0)
				for _, disk := range *a2aDetails.ProtectedManagedDisks {
					diskOutput := make(map[string]interface{})
					diskId := ""
					if disk.DiskId != nil {
						diskId = *disk.DiskId
					}
					diskOutput["disk_id"] = diskId

					primaryStagingAzureStorageAccountID := ""
					if disk.PrimaryStagingAzureStorageAccountId != nil {
						primaryStagingAzureStorageAccountID = *disk.PrimaryStagingAzureStorageAccountId
					}
					diskOutput["staging_storage_account_id"] = primaryStagingAzureStorageAccountID

					recoveryResourceGroupID := ""
					if disk.RecoveryResourceGroupId != nil {
						recoveryResourceGroupID = *disk.RecoveryResourceGroupId
					}
					diskOutput["target_resource_group_id"] = recoveryResourceGroupID

					recoveryReplicaDiskAccountType := ""
					if disk.RecoveryReplicaDiskAccountType != nil {
						recoveryReplicaDiskAccountType = *disk.RecoveryReplicaDiskAccountType
					}
					diskOutput["target_replica_disk_type"] = recoveryReplicaDiskAccountType

					recoveryTargetDiskAccountType := ""
					if disk.RecoveryTargetDiskAccountType != nil {
						recoveryTargetDiskAccountType = *disk.RecoveryTargetDiskAccountType
					}
					diskOutput["target_disk_type"] = recoveryTargetDiskAccountType

					recoveryEncryptionSetId := ""
					if disk.RecoveryDiskEncryptionSetId != nil {
						recoveryEncryptionSetId = *disk.RecoveryDiskEncryptionSetId
					}
					diskOutput["target_disk_encryption_set_id"] = recoveryEncryptionSetId

					diskOutput["target_disk_encryption"] = flattenTargetDiskEncryption(disk)

					disksOutput = append(disksOutput, diskOutput)
				}
				d.Set("managed_disk", pluginsdk.NewSet(resourceSiteRecoveryReplicatedVMDiskHash, disksOutput))
			}

			if a2aDetails.VMNics != nil {
				nicsOutput := make([]interface{}, 0)
				for _, nic := range *a2aDetails.VMNics {
					nicOutput := make(map[string]interface{})
					if nic.SourceNicArmId != nil {
						nicOutput["source_network_interface_id"] = *nic.SourceNicArmId
					}
					if nic.IPConfigs != nil && len(*(nic.IPConfigs)) > 0 {
						ipConfig := (*(nic.IPConfigs))[0]
						if ipConfig.RecoveryStaticIPAddress != nil {
							nicOutput["target_static_ip"] = *ipConfig.RecoveryStaticIPAddress
						}
						if ipConfig.RecoverySubnetName != nil {
							nicOutput["target_subnet_name"] = *ipConfig.RecoverySubnetName
						}
						if ipConfig.RecoveryPublicIPAddressId != nil {
							nicOutput["recovery_public_ip_address_id"] = *ipConfig.RecoveryPublicIPAddressId
						}
					}
					nicsOutput = append(nicsOutput, nicOutput)
				}
				d.Set("network_interface", pluginsdk.NewSet(pluginsdk.HashResource(networkInterfaceResource()), nicsOutput))
			}
		}
	}

	return nil
}

func resourceSiteRecoveryReplicatedItemDelete(d *pluginsdk.ResourceData, meta interface{}) error {
	id, err := replicationprotecteditems.ParseReplicationProtectedItemID(d.Id())
	if err != nil {
		return err
	}

	client := meta.(*clients.Client).RecoveryServices.ReplicationProtectedItemsClient

	disableProtectionReason := replicationprotecteditems.DisableProtectionReasonNotSpecified

	disableProtectionInput := replicationprotecteditems.DisableProtectionInput{
		Properties: replicationprotecteditems.DisableProtectionInputProperties{
			DisableProtectionReason: &disableProtectionReason,
			// It's a workaround for https://github.com/hashicorp/pandora/issues/1864
			ReplicationProviderInput: &siterecovery.DisableProtectionProviderSpecificInput{
				InstanceType: siterecovery.InstanceTypeDisableProtectionProviderSpecificInput,
			},
		},
	}

	ctx, cancel := timeouts.ForDelete(meta.(*clients.Client).StopContext, d)
	defer cancel()
	err = client.DeleteThenPoll(ctx, *id, disableProtectionInput)
	if err != nil {
		return fmt.Errorf("deleting site recovery replicated vm %s : %+v", id.String(), err)
	}

	return nil
}

func resourceSiteRecoveryReplicatedVMDiskHash(v interface{}) int {
	var buf bytes.Buffer

	if m, ok := v.(map[string]interface{}); ok {
		if v, ok := m["disk_id"]; ok {
			buf.WriteString(strings.ToLower(v.(string)))
		}
	}

	return pluginsdk.HashString(buf.String())
}

func waitForReplicationToBeHealthy(ctx context.Context, d *pluginsdk.ResourceData, meta interface{}) (*replicationprotecteditems.ReplicationProtectedItem, error) {
	log.Printf("Waiting for Site Recover to replicate VM.")
	stateConf := &pluginsdk.StateChangeConf{
		Target:       []string{"Protected"},
		Refresh:      waitForReplicationToBeHealthyRefreshFunc(d, meta),
		PollInterval: time.Minute,
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, fmt.Errorf("context had no deadline")
	}
	stateConf.Timeout = time.Until(deadline)

	result, err := stateConf.WaitForStateContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("waiting for site recovery to replicate vm: %+v", err)
	}

	protectedItem, ok := result.(replicationprotecteditems.ReplicationProtectedItem)
	if ok {
		return &protectedItem, nil
	} else {
		return nil, fmt.Errorf("waiting for site recovery return incompatible type")
	}
}

func waitForReplicationToBeHealthyRefreshFunc(d *pluginsdk.ResourceData, meta interface{}) pluginsdk.StateRefreshFunc {
	return func() (interface{}, string, error) {
		id, err := replicationprotecteditems.ParseReplicationProtectedItemID(d.Id())
		if err != nil {
			return nil, "", err
		}

		client := meta.(*clients.Client).RecoveryServices.ReplicationProtectedItemsClient

		ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
		defer cancel()

		resp, err := client.Get(ctx, *id)
		if err != nil {
			return nil, "", fmt.Errorf("making Read request on site recovery replicated vm %s : %+v", id.String(), err)
		}

		if resp.Model == nil {
			return nil, "", fmt.Errorf("Missing Model in response when making Read request on site recovery replicated vm %s  %+v", id.String(), err)
		}

		if resp.Model.Properties == nil {
			return nil, "", fmt.Errorf("Missing Properties in response when making Read request on site recovery replicated vm %s  %+v", id.String(), err)
		}

		if resp.Model.Properties.ProviderSpecificDetails == nil {
			return nil, "", fmt.Errorf("Missing Properties.ProviderSpecificDetails in response when making Read request on site recovery replicated vm %s : %+v", id.String(), err)
		}

		// Find first disk that is not fully replicated yet
		if a2aDetails, isA2a := resp.Model.Properties.ProviderSpecificDetails.(replicationprotecteditems.A2AReplicationDetails); isA2a {
			if a2aDetails.MonitoringPercentageCompletion != nil {
				log.Printf("Waiting for Site Recover to replicate VM, %d%% complete.", *a2aDetails.MonitoringPercentageCompletion)
			}
			if a2aDetails.VMProtectionState != nil {
				return *resp.Model, *a2aDetails.VMProtectionState, nil
			}
		}

		if resp.Model.Properties.ReplicationHealth == nil {
			return nil, "", fmt.Errorf("missing ReplicationHealth in response when making Read request on site recovery replicated vm %s : %+v", id.String(), err)
		}
		return *resp.Model, *resp.Model.Properties.ReplicationHealth, nil
	}
}

func expandDiskEncryption(diskEncryptionInfoList []interface{}) *replicationprotecteditems.DiskEncryptionInfo {
	if len(diskEncryptionInfoList) == 0 {
		return &replicationprotecteditems.DiskEncryptionInfo{}
	}
	diskEncryptionInfoMap := diskEncryptionInfoList[0].(map[string]interface{})

	dek := diskEncryptionInfoMap["disk_encryption_key"].([]interface{})[0].(map[string]interface{})
	diskEncryptionInfo := &replicationprotecteditems.DiskEncryptionInfo{
		DiskEncryptionKeyInfo: &replicationprotecteditems.DiskEncryptionKeyInfo{
			SecretIdentifier:      utils.String(dek["secret_url"].(string)),
			KeyVaultResourceArmId: utils.String(dek["vault_id"].(string)),
		},
	}

	if keyEncryptionKey := diskEncryptionInfoMap["key_encryption_key"].([]interface{}); len(keyEncryptionKey) > 0 {
		kek := keyEncryptionKey[0].(map[string]interface{})
		diskEncryptionInfo.KeyEncryptionKeyInfo = &replicationprotecteditems.KeyEncryptionKeyInfo{
			KeyIdentifier:         utils.String(kek["key_url"].(string)),
			KeyVaultResourceArmId: utils.String(kek["vault_id"].(string)),
		}
	}

	return diskEncryptionInfo
}

func flattenTargetDiskEncryption(disk replicationprotecteditems.A2AProtectedManagedDiskDetails) []interface{} {
	secretUrl := ""
	dekVaultId := ""
	keyUrl := ""
	kekVaultId := ""

	if disk.SecretIdentifier != nil {
		secretUrl = *disk.SecretIdentifier
	}
	if disk.DekKeyVaultArmId != nil {
		dekVaultId = *disk.DekKeyVaultArmId
	}
	if disk.KeyIdentifier != nil {
		keyUrl = *disk.KeyIdentifier
	}
	if disk.KekKeyVaultArmId != nil {
		kekVaultId = *disk.KekKeyVaultArmId
	}

	if secretUrl == "" && dekVaultId == "" && keyUrl == "" && kekVaultId == "" {
		return []interface{}{}
	}

	diskEncryptionKeys := make([]interface{}, 0)
	if secretUrl != "" || dekVaultId != "" {
		diskEncryptionKeys = append(diskEncryptionKeys, map[string]interface{}{
			"secret_url": secretUrl,
			"vault_id":   dekVaultId,
		})
	}

	keyEncryptionKeys := make([]interface{}, 0)
	if keyUrl != "" || kekVaultId != "" {
		keyEncryptionKeys = append(keyEncryptionKeys, map[string]interface{}{
			"key_url":  keyUrl,
			"vault_id": kekVaultId,
		})
	}

	return []interface{}{
		map[string]interface{}{
			"disk_encryption_key": diskEncryptionKeys,
			"key_encryption_key":  keyEncryptionKeys,
		},
	}
}
