provider "azuread" {
  version = "~> 0.4"
}

provider "azurerm" {
  version = "~> 1.31"
}

provider "random" {
  version = "~> 2.1"
}

variable "location" {
  description = "Azure datacenter to deploy to."
  default = "westus2"
}

variable "servicebus_name_prefix" {
  description = "Input your unique Azure Service Bus Namespace name"
  default = "azuresbtests"
}

variable "resource_group_name_prefix" {
  description = "Resource group to provision test infrastructure in."
  default = "servicebus-go-tests"
}

variable "azure_client_secret" {
  description = "(Optional) piped in from env var so .env will be updated if there is an existing client secret"
  default     = "foo"
}

resource "random_string" "name" {
  length  = 8
  upper   = false
  special = false
  number  = false
}

# Create resource group for all of the things
resource "azurerm_resource_group" "test" {
  name      = "${var.resource_group_name_prefix}-${random_string.name.result}"
  location  = var.location
}

resource "azurerm_servicebus_namespace" "test" {
  name = "${var.servicebus_name_prefix}-${random_string.name.result}"
  location = azurerm_resource_group.test.location
  resource_group_name = azurerm_resource_group.test.name
  sku = "standard"
}

# Generate a random secret fo the service principal
resource "random_string" "secret" {
  count   = data.azurerm_client_config.current.service_principal_application_id == "" ? 1 : 0
  length  = 32
  upper   = true
  special = true
  number  = true
}

// Application for AAD authentication
resource "azuread_application" "test" {
  count                       = data.azurerm_client_config.current.service_principal_application_id == "" ? 1 : 0
  name                        = "servicebustest"
  homepage                    = "https://servicebustest"
  identifier_uris             = ["https://servicebustest"]
  reply_urls                  = ["https://servicebustest"]
  available_to_other_tenants  = false
  oauth2_allow_implicit_flow  = true
}

# Create a service principal, which represents a linkage between the AAD application and the password
resource "azuread_service_principal" "test" {
  count          = data.azurerm_client_config.current.service_principal_application_id == "" ? 1 : 0
  application_id = azuread_application.test[0].application_id
}

# Create a new service principal password which will be the AZURE_CLIENT_SECRET env var
resource "azuread_service_principal_password" "test" {
  count                 = data.azurerm_client_config.current.service_principal_application_id == "" ? 1 : 0
  service_principal_id  = azuread_service_principal.test[0].id
  value                 = random_string.secret[0].result
  end_date              = "2030-01-01T01:02:03Z"
}

# This provides the new AAD application the rights to managed, send and receive from the Event Hubs instance
resource "azurerm_role_assignment" "service_principal_eh" {
  count                 = data.azurerm_client_config.current.service_principal_application_id == "" ? 1 : 0
  scope                 = "subscriptions/${data.azurerm_client_config.current.subscription_id}/resourceGroups/${azurerm_resource_group.test.name}/providers/Microsoft.ServiceBus/namespaces/${azurerm_servicebus_namespace.test.name}"
  role_definition_name  = "Owner"
  principal_id          = azuread_service_principal.test[0].id
}

# This provides the new AAD application the rights to managed the resource group
resource "azurerm_role_assignment" "service_principal_rg" {
  count                 = data.azurerm_client_config.current.service_principal_application_id == "" ? 1 : 0
  scope                 = "subscriptions/${data.azurerm_client_config.current.subscription_id}/resourceGroups/${azurerm_resource_group.test.name}"
  role_definition_name  = "Owner"
  principal_id          = azuread_service_principal.test[0].id
}

# Most tests should create and destroy their own Queues, Topics, and Subscriptions. However, to keep examples from being
# bloated, the items below are created externally by Terraform.

resource "azurerm_servicebus_queue" "scheduledMessages" {
  name = "scheduledmessages"
  resource_group_name = azurerm_resource_group.test.name
  namespace_name = azurerm_servicebus_namespace.test.name
}

resource "azurerm_servicebus_queue" "queueSchedule" {
  name = "schedulewithqueue"
  resource_group_name = azurerm_resource_group.test.name
  namespace_name = azurerm_servicebus_namespace.test.name
}

resource "azurerm_servicebus_queue" "helloworld" {
  name = "helloworld"
  resource_group_name = azurerm_resource_group.test.name
  namespace_name = azurerm_servicebus_namespace.test.name
}

resource "azurerm_servicebus_queue" "receiveSession" {
  name = "receivesession"
  resource_group_name = azurerm_resource_group.test.name
  namespace_name = azurerm_servicebus_namespace.test.name
  default_message_ttl = "PT300S"
  requires_session = true
}

# Data resources used to get SubID and Tennant Info
data "azurerm_client_config" "current" {}

output "TEST_SERVICEBUS_RESOURCE_GROUP" {
  value = azurerm_resource_group.test.name
}

output "SERVICEBUS_CONNECTION_STRING" {
  value = "Endpoint=sb://${azurerm_servicebus_namespace.test.name}.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=${azurerm_servicebus_namespace.test.default_primary_key}"
  sensitive = true
}

output "AZURE_SUBSCRIPTION_ID" {
  value = data.azurerm_client_config.current.subscription_id
}

output "TEST_SERVICEBUS_LOCATION" {
  value = azurerm_servicebus_namespace.test.location
}

output "AZURE_TENANT_ID" {
  value = data.azurerm_client_config.current.tenant_id
}

output "AZURE_CLIENT_ID" {
  value = compact(concat(azuread_application.test.*.application_id, list(data.azurerm_client_config.current.client_id)))[0]
}

output "AZURE_CLIENT_SECRET" {
  value     = compact(concat(azuread_service_principal_password.test.*.value, list(var.azure_client_secret)))[0]
  sensitive = true
}