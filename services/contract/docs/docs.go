// Package docs exposes the OpenAPI document served by the contract service.
package docs

import "github.com/swaggo/swag"

const docTemplate = `{
    "schemes": {{ marshal .Schemes }},
    "swagger": "2.0",
    "info": {
        "description": "{{escape .Description}}",
        "title": "{{.Title}}",
        "contact": {
            "name": "API Support",
            "url": "http://www.swagger.io/support",
            "email": "support@swagger.io"
        },
        "license": {
            "name": "Apache 2.0",
            "url": "http://www.apache.org/licenses/LICENSE-2.0.html"
        },
        "version": "{{.Version}}"
    },
    "host": "{{.Host}}",
    "basePath": "{{.BasePath}}",
    "paths": {
        "/create-contract": {
            "post": {
                "description": "Creates a contract from the supplied account, client, tenant and product IDs.",
                "consumes": ["application/json"],
                "produces": ["application/json"],
                "summary": "Create a contract",
                "parameters": [{
                    "description": "Contract data",
                    "name": "contract",
                    "in": "body",
                    "required": true,
                    "schema": {"$ref": "#/definitions/CreateContractRequest"}
                }],
                "responses": {
                    "201": {"description": "Created", "schema": {"$ref": "#/definitions/CreateContractResponse"}},
                    "400": {"description": "Invalid request", "schema": {"$ref": "#/definitions/ErrorResponse"}},
                    "500": {"description": "Storage failure", "schema": {"$ref": "#/definitions/ErrorResponse"}}
                }
            }
        }
    },
    "definitions": {
        "CreateContractRequest": {
            "type": "object",
            "properties": {
                "accountId": {"type": "string", "format": "uuid"},
                "clientId": {"type": "string", "format": "uuid"},
                "tenantId": {"type": "string", "format": "uuid"},
                "productId": {"type": "string", "format": "uuid"},
                "photos": {"type": "array", "items": {"type": "string"}}
            },
            "required": ["accountId", "clientId", "tenantId", "productId"]
        },
        "CreateContractResponse": {
            "type": "object",
            "properties": {"contractId": {"type": "string", "format": "uuid"}}
        },
        "ErrorResponse": {
            "type": "object",
            "properties": {"error": {"type": "string"}}
        }
    }
}`

// SwaggerInfo holds the API metadata. The service sets Host during startup.
var SwaggerInfo = &swag.Spec{
	Version:          "1.0",
	Host:             "localhost:4000",
	BasePath:         "/",
	Schemes:          []string{"http"},
	Title:            "Contract Service API",
	Description:      "Contract Service API",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  docTemplate,
	LeftDelim:        "{{",
	RightDelim:       "}}",
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
