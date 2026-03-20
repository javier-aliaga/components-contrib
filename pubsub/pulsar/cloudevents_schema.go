/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pulsar

import (
	"encoding/json"
	"fmt"
)

// wrapInCloudEventsAvroSchema takes a user-provided inner Avro schema JSON string
// and returns a CloudEvents envelope Avro schema with the inner schema embedded
// as the "data" field type. This ensures the Pulsar Schema Registry entry matches
// the actual wire format when Dapr wraps payloads in CloudEvents envelopes.
//
// The generated schema follows the CloudEvents Avro format specification:
// https://github.com/cloudevents/spec/blob/main/cloudevents/bindings/avro-format.md
func wrapInCloudEventsAvroSchema(innerSchemaJSON string) (string, error) {
	var innerSchema interface{}
	if err := json.Unmarshal([]byte(innerSchemaJSON), &innerSchema); err != nil {
		return "", fmt.Errorf("failed to parse inner Avro schema: %w", err)
	}

	envelope := map[string]interface{}{
		"type":      "record",
		"name":      "CloudEvent",
		"namespace": "io.cloudevents",
		"fields": []interface{}{
			avroField("id", "string"),
			avroField("source", "string"),
			avroField("specversion", "string"),
			avroField("type", "string"),
			avroNullableField("datacontenttype", "string"),
			avroNullableField("subject", "string"),
			avroNullableField("time", "string"),
			avroNullableField("topic", "string"),
			avroNullableField("pubsubname", "string"),
			avroNullableField("traceid", "string"),
			avroNullableField("traceparent", "string"),
			avroNullableField("tracestate", "string"),
			avroNullableField("expiration", "string"),
			avroNullableField("data", innerSchema),
			avroNullableField("data_base64", "string"),
		},
	}

	result, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("failed to marshal CloudEvents envelope schema: %w", err)
	}

	return string(result), nil
}

func avroField(name, typ string) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"type": typ,
	}
}

func avroNullableField(name string, typ interface{}) map[string]interface{} {
	return map[string]interface{}{
		"name":    name,
		"type":    []interface{}{"null", typ},
		"default": nil,
	}
}
