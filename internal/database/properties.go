package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxPropertyNodes = 250000

func propertiesOf(value any) ([]Property, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("provider properties exceed 16 MiB")
	}
	return propertiesJSON(data)
}

func propertiesJSON(data []byte) ([]Property, error) {
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("provider properties exceed 16 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	count := 0
	root, err := propertyValue("", value, 0, &count)
	if err != nil {
		return nil, err
	}
	return root.Children, nil
}

func propertyValue(name string, value any, depth int, count *int) (Property, error) {
	*count++
	if depth > 100 || *count > maxPropertyNodes {
		return Property{}, fmt.Errorf("provider property structure exceeds limits")
	}
	p := Property{Name: name}
	if sensitiveProperty(name) {
		p.Value = "[redacted]"
		return p, nil
	}
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, err := propertyValue(key, v[key], depth+1, count)
			if err != nil {
				return p, err
			}
			p.Children = append(p.Children, child)
		}
		if len(keys) == 0 {
			p.Value = "{}"
		}
	case []any:
		for i, item := range v {
			child, err := propertyValue(fmt.Sprint(i+1), item, depth+1, count)
			if err != nil {
				return p, err
			}
			p.Children = append(p.Children, child)
		}
		if len(v) == 0 {
			p.Value = "[]"
		}
	case nil:
		p.Value = "NULL"
	case string:
		p.Value = redactPropertyString(v)
	default:
		p.Value = fmt.Sprint(v)
	}
	return p, nil
}

func sensitiveProperty(name string) bool {
	key := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
	for _, s := range []string{"password", "privatekey", "accesstoken", "refreshtoken", "credentials", "clientsecret", "secretaccesskey", "authorization"} {
		if strings.Contains(key, s) {
			return true
		}
	}
	return key == "token" || key == "secret" || key == "passfile"
}

func redactPropertyString(value string) string {
	if key, _, ok := strings.Cut(value, "="); ok && !strings.Contains(key, "://") && sensitiveProperty(strings.TrimSpace(key)) {
		return key + "=[redacted]"
	}
	if strings.Contains(value, "://") {
		if u, err := url.Parse(value); err == nil && u.Scheme != "" {
			if u.User != nil {
				if _, set := u.User.Password(); set {
					u.User = url.UserPassword(u.User.Username(), "[redacted]")
				}
			}
			q := u.Query()
			for k := range q {
				if sensitiveProperty(k) || strings.Contains(strings.ToLower(k), "signature") {
					q.Set(k, "[redacted]")
				}
			}
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return value
}

func property(name string, value any) Property { return Property{Name: name, Value: fmt.Sprint(value)} }

func (m *TableMetadata) section(name, state, message string) {
	if m.Sections == nil {
		m.Sections = map[string]MetadataSection{}
	}
	m.Sections[name] = MetadataSection{State: state, Message: message, FetchedAt: time.Now().UTC()}
}

func (m *TableMetadata) SectionItems(name string) []Definition {
	switch name {
	case "indexes":
		return m.Indexes
	case "partitions":
		return m.Partitions
	case "constraints":
		return m.Constraints
	case "triggers":
		return m.Triggers
	case "policies":
		return m.Policies
	case "privileges":
		return m.Privileges
	case "rules":
		return m.Rules
	}
	return nil
}

func (m *TableMetadata) setSectionItems(name string, items []Definition) {
	switch name {
	case "indexes":
		m.Indexes = items
	case "partitions":
		m.Partitions = items
	case "policies":
		m.Policies = items
	case "privileges":
		m.Privileges = items
	case "rules":
		m.Rules = items
	}
}
