package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	bqapi "google.golang.org/api/bigquery/v2"
)

// TableSection loads optional, potentially separately permissioned metadata.
func (c *Client) TableSection(ctx context.Context, t Table, section string) ([]Definition, error) {
	if c.Engine() != BigQuery {
		return nil, fmt.Errorf("section is provided with base table metadata")
	}
	switch section {
	case "partitions":
		return c.bigQueryMetadataRows(ctx, t, "PARTITIONS", "partition_id", "PARTITION")
	case "indexes":
		search, err := c.bigQueryMetadataRows(ctx, t, "SEARCH_INDEXES", "index_name", "SEARCH INDEX")
		if err != nil {
			return nil, err
		}
		vector, err := c.bigQueryMetadataRows(ctx, t, "VECTOR_INDEXES", "index_name", "VECTOR INDEX")
		if err != nil {
			return nil, err
		}
		if len(search)+len(vector) > CatalogLimit {
			return nil, fmt.Errorf("index metadata exceeds catalog limit")
		}
		return append(search, vector...), nil
	case "policies":
		var result []Definition
		token := ""
		for {
			page, err := c.bqAPI.RowAccessPolicies.List(t.Project, t.Schema, t.Name).PageSize(1000).PageToken(token).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			for _, policy := range page.RowAccessPolicies {
				if policy == nil {
					continue
				}
				name := "Row access policy"
				if policy.RowAccessPolicyReference != nil {
					name = policy.RowAccessPolicyReference.PolicyId
				}
				props, err := propertiesOf(policy)
				if err != nil {
					return nil, err
				}
				result = append(result, Definition{Name: name, Kind: "ROW ACCESS POLICY", SQL: policy.FilterPredicate, Properties: props})
				if len(result) > CatalogLimit {
					return nil, fmt.Errorf("policy metadata exceeds catalog limit")
				}
			}
			if page.NextPageToken == "" {
				break
			}
			if page.NextPageToken == token {
				return nil, fmt.Errorf("repeated policy pagination token")
			}
			token = page.NextPageToken
		}
		return result, nil
	case "privileges":
		resource := "projects/" + t.Project + "/datasets/" + t.Schema + "/tables/" + t.Name
		policy, err := c.bqAPI.Tables.GetIamPolicy(resource, &bqapi.GetIamPolicyRequest{Options: &bqapi.GetPolicyOptions{RequestedPolicyVersion: 3}}).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		var result []Definition
		for _, binding := range policy.Bindings {
			if binding == nil {
				continue
			}
			props, err := propertiesOf(binding)
			if err != nil {
				return nil, err
			}
			result = append(result, Definition{Name: binding.Role, Kind: "IAM BINDING", Properties: props})
			if len(result) > CatalogLimit {
				return nil, fmt.Errorf("privilege metadata exceeds catalog limit")
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("unknown table section %q", section)
}

func (c *Client) bigQueryMetadataRows(ctx context.Context, t Table, view, nameField, kind string) ([]Definition, error) {
	literal := "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(t.Name) + "'"
	ref := bqIdentifier(t.Project + "." + t.Schema + ".INFORMATION_SCHEMA." + view)
	query := "SELECT TO_JSON_STRING(m) AS metadata FROM " + ref + " m WHERE table_name=" + literal
	if view == "PARTITIONS" {
		query += " AND partition_id IS NOT NULL ORDER BY partition_id,storage_tier"
	} else {
		query += " ORDER BY index_name"
	}
	rows, err := c.catalogQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	var result []Definition
	for _, row := range rows.Rows {
		if len(row) != 1 {
			return nil, fmt.Errorf("unexpected metadata result shape")
		}
		var values map[string]json.RawMessage
		if err = json.Unmarshal([]byte(row[0]), &values); err != nil {
			return nil, err
		}
		var name string
		if err = json.Unmarshal(values[nameField], &name); err != nil {
			return nil, fmt.Errorf("metadata missing %s", nameField)
		}
		if view == "PARTITIONS" {
			var tier string
			_ = json.Unmarshal(values["storage_tier"], &tier)
			if tier != "" {
				name += " · " + tier
			}
		}
		props, err := propertiesJSON([]byte(row[0]))
		if err != nil {
			return nil, err
		}
		result = append(result, Definition{Name: name, Kind: kind, Properties: props})
	}
	return result, nil
}
