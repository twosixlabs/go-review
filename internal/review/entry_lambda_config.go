package review

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type lambdaBuild struct {
	output, directory string
	evidence          EntryPointEvidence
}

type lambdaTrigger struct {
	name, kind, output, details string
	evidence                    EntryPointEvidence
}

const lambdaConfigurationLimit = 4096

var lambdaBuildCommand = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=[A-Za-z0-9_./-]+[[:space:]]+)*go[[:space:]]+build[[:space:]]+-o[[:space:]]+([A-Za-z0-9_./-]+)[[:space:]]+([A-Za-z0-9_./-]+)[[:space:]]*$`)
var lambdaDirectoryChange = regexp.MustCompile(`(?:^|[[:space:];&|])(?:cd|pushd|popd)(?:[[:space:];&|]|$)`)

func lambdaConfigEntries(ctx context.Context, r *Report, registrations []EntryPoint, inv *EntryPointInventory) ([]EntryPoint, error) {
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("Lambda source root is a symlink: %s", root)
	}
	var builds []lambdaBuild
	var buildSources []Document
	var triggers []lambdaTrigger
	buildLimit, triggerLimit, fileLimit := false, false, false
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			inv.Limitations = append(inv.Limitations, "Cannot inspect Lambda configuration: "+walkErr.Error())
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && r.Scope.pathReason(r.Scope.relative(root, rel)) != "" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			inv.Limitations = append(inv.Limitations, "Skipped symlink in Lambda configuration discovery: "+rel)
			return nil
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Lstat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		scanned++
		if scanned > wasmFileLimit {
			fileLimit = true
			inv.Limitations = append(inv.Limitations, "Lambda configuration scan reached the 4096 file limit.")
			return fs.SkipAll
		}
		name := d.Name()
		isConfig := name == "serverless.yml" || name == "serverless.yaml" || name == "template.yml" || name == "template.yaml"
		isBuild := rel == "Makefile" || rel == "makefile" || rel == ".gitlab-ci.yml" || rel == ".gitlab-ci.yaml"
		if !isConfig && !isBuild {
			return nil
		}
		data, err := wasmReadFile(root, path)
		if err != nil {
			inv.Limitations = append(inv.Limitations, "Cannot read Lambda configuration "+rel+": "+err.Error())
			return nil
		}
		doc := Document{ID: stableID("doc", rel), Path: rel, SHA256: digest(data), Text: string(data)}
		if isBuild {
			before := len(builds)
			addBuild := func(line string, lineNumber int) {
				command := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "@-"))
				match := lambdaBuildCommand.FindStringSubmatch(command)
				if match == nil || !strings.HasPrefix(match[2], ".") {
					return
				}
				if len(builds) == lambdaConfigurationLimit {
					buildLimit = true
					return
				}
				builds = append(builds, lambdaBuild{output: filepath.Clean(match[1]), directory: filepath.Clean(match[2]),
					evidence: EntryPointEvidence{SourceID: doc.ID, Line: lineNumber, Kind: "lambda-build", Detail: strings.TrimSpace(line)}})
			}
			if strings.HasPrefix(name, ".gitlab-ci.") {
				var ci yaml.Node
				if err := yaml.Unmarshal(data, &ci); err != nil {
					inv.Limitations = append(inv.Limitations, "Cannot parse Lambda build configuration "+rel+": "+err.Error())
					return nil
				}
				if lambdaDirectoryChange.MatchString(doc.Text) {
					inv.Limitations = append(inv.Limitations, "GitLab build working directory may change; build links were not inferred from "+rel+".")
				} else {
					lambdaCIScripts(&ci, addBuild)
				}
			} else {
				for i, line := range strings.Split(doc.Text, "\n") {
					if !strings.HasPrefix(line, "\t") {
						continue
					}
					addBuild(line, i+1)
				}
			}
			if len(builds) > before {
				buildSources = append(buildSources, doc)
			}
			return nil
		}
		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			inv.Limitations = append(inv.Limitations, "Cannot parse Lambda configuration "+rel+": "+err.Error())
			return nil
		}
		found, truncated := lambdaParseConfig(&node, doc, lambdaConfigurationLimit-len(triggers))
		triggerLimit = triggerLimit || truncated
		if len(found) > 0 {
			triggers = append(triggers, found...)
			inv.Sources = append(inv.Sources, doc)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if buildLimit {
		inv.Limitations = append(inv.Limitations, "Lambda build evidence reached the 4096 command limit.")
	}
	if triggerLimit {
		inv.Limitations = append(inv.Limitations, "Lambda trigger configuration reached the 4096 entry limit.")
	}
	mainDirectories := map[string]string{}
	files := map[string]File{}
	for _, file := range r.Files {
		files[file.ID] = file
	}
	for _, symbol := range r.Symbols {
		if symbol.Name == "main" && symbol.Kind == "function" {
			if file, ok := files[symbol.Location.FileID]; ok {
				mainDirectories[symbol.ID] = filepath.Clean(filepath.Dir(file.Path))
			}
		}
	}
	var result []EntryPoint
	matched := map[string]bool{}
	usedBuilds := map[string]bool{}
	for _, trigger := range triggers {
		entry := EntryPoint{ID: stableID("entry", fmt.Sprintf("lambda-trigger:%s:%d:%d:%s", trigger.evidence.SourceID, trigger.evidence.Line, trigger.evidence.Column, trigger.name)),
			Kind: "lambda", Name: trigger.name, Resolution: "unresolved", Reason: "No unique static build and Lambda registration link was found.",
			Trigger: trigger.kind, TriggerDetails: trigger.details, Evidence: []EntryPointEvidence{trigger.evidence}}
		directories := map[string]bool{}
		var matchingBuilds []lambdaBuild
		if trigger.output != "" {
			for _, build := range builds {
				if trigger.output != build.output {
					continue
				}
				directories[build.directory] = true
				matchingBuilds = append(matchingBuilds, build)
			}
		}
		var matches []EntryPoint
		if len(directories) == 1 && !buildLimit && !fileLimit {
			for _, registration := range registrations {
				if directories[mainDirectories[registration.BoundarySymbolID]] {
					matches = append(matches, registration)
				}
			}
		}
		for _, build := range matchingBuilds {
			entry.Evidence = append(entry.Evidence, build.evidence)
			usedBuilds[build.evidence.SourceID] = true
		}
		if len(matches) == 1 {
			m := matches[0]
			entry.SymbolID, entry.BoundarySymbolID = m.SymbolID, m.BoundarySymbolID
			entry.Resolution, entry.Reason = m.Resolution, m.Reason
			entry.Evidence = append(entry.Evidence, m.Evidence...)
			matched[m.ID] = true
		}
		result = append(result, entry)
	}
	for _, source := range buildSources {
		if usedBuilds[source.ID] {
			inv.Sources = append(inv.Sources, source)
		}
	}
	for _, registration := range registrations {
		if !matched[registration.ID] {
			result = append(result, registration)
		}
	}
	return result, nil
}

func lambdaCIScripts(node *yaml.Node, add func(string, int)) {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Value == "script" && value.Kind == yaml.SequenceNode {
				for _, command := range value.Content {
					if command.Kind == yaml.ScalarNode && command.Tag == "!!str" {
						add(command.Value, command.Line)
					}
				}
			} else {
				lambdaCIScripts(value, add)
			}
		}
		return
	}
	for _, child := range node.Content {
		lambdaCIScripts(child, add)
	}
}

func lambdaYAMLValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func lambdaScalar(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.ContainsAny(node.Value, "$`") {
		return ""
	}
	return node.Value
}

func lambdaParseConfig(document *yaml.Node, doc Document, limit int) ([]lambdaTrigger, bool) {
	if len(document.Content) == 0 {
		return nil, false
	}
	root := document.Content[0]
	var triggers []lambdaTrigger
	truncated := false
	add := func(name, kind, output string, node, details *yaml.Node) {
		if len(triggers) >= limit {
			truncated = true
			return
		}
		triggers = append(triggers, lambdaTrigger{name: name, kind: kind, output: output,
			details:  lambdaYAMLSource(doc.Text, details),
			evidence: EntryPointEvidence{SourceID: doc.ID, Line: node.Line, Column: node.Column, Kind: "lambda-trigger", Detail: kind}})
	}
	functions := lambdaYAMLValue(root, "functions")
	provider := lambdaYAMLValue(root, "provider")
	if name := lambdaYAMLValue(provider, "name"); lambdaScalar(provider) == "aws" || lambdaScalar(name) == "aws" {
		if functions != nil && functions.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(functions.Content); i += 2 {
				name, function := functions.Content[i].Value, functions.Content[i+1]
				output := lambdaScalar(lambdaYAMLValue(function, "handler"))
				if output != "" {
					output = filepath.Clean(filepath.Join(filepath.Dir(doc.Path), output))
				}
				events := lambdaYAMLValue(function, "events")
				if events == nil {
					continue
				}
				if events.Kind != yaml.SequenceNode {
					add(name+" · unknown", "unknown", "", events, events)
					continue
				}
				for _, event := range events.Content {
					if event.Kind != yaml.MappingNode {
						add(name+" · unknown", "unknown", "", event, event)
						continue
					}
					for j := 0; j+1 < len(event.Content); j += 2 {
						kind := event.Content[j].Value
						eventOutput := output
						if lambdaScalar(event.Content[j]) == "" {
							kind, eventOutput = "unknown", ""
						}
						add(name+" · "+kind, kind, eventOutput, event.Content[j], event)
					}
				}
			}
		}
	}
	resources := lambdaYAMLValue(root, "Resources")
	if resources != nil && resources.Kind == yaml.MappingNode {
		outputs := map[string]string{}
		resourceTypes := map[string]string{}
		for i := 0; i+1 < len(resources.Content); i += 2 {
			name, resource := resources.Content[i].Value, resources.Content[i+1]
			typ := lambdaScalar(lambdaYAMLValue(resource, "Type"))
			resourceTypes[name] = typ
			if typ != "AWS::Serverless::Function" {
				continue
			}
			props := lambdaYAMLValue(resource, "Properties")
			handler := lambdaScalar(lambdaYAMLValue(props, "Handler"))
			code := lambdaYAMLValue(props, "CodeUri")
			codeDir := "."
			if code != nil {
				codeDir = lambdaScalar(code)
			}
			output := ""
			if handler != "" && codeDir != "" {
				output = filepath.Clean(filepath.Join(filepath.Dir(doc.Path), codeDir, handler))
			}
			outputs[name] = output
			events := lambdaYAMLValue(props, "Events")
			if events == nil {
				continue
			}
			if events.Kind != yaml.MappingNode || events.Tag != "!!map" {
				add(name+" · unknown", "unknown", "", events, events)
				continue
			}
			for j := 0; j+1 < len(events.Content); j += 2 {
				eventName, event := events.Content[j].Value, events.Content[j+1]
				kindNode := lambdaYAMLValue(event, "Type")
				kind := lambdaScalar(kindNode)
				if kind == "" {
					kind, kindNode = "unknown", events.Content[j]
					add(name+" · "+eventName+" · "+kind, kind, "", kindNode, event)
					continue
				}
				add(name+" · "+eventName+" · "+kind, kind, output, kindNode, event)
			}
		}
		for i := 0; i+1 < len(resources.Content); i += 2 {
			name, resource := resources.Content[i].Value, resources.Content[i+1]
			typ := lambdaScalar(lambdaYAMLValue(resource, "Type"))
			props := lambdaYAMLValue(resource, "Properties")
			if typ == "AWS::Lambda::EventSourceMapping" {
				ref := lambdaReference(lambdaYAMLValue(props, "FunctionName"))
				source := lambdaYAMLValue(props, "EventSourceArn")
				kind := lambdaEventSource(source, resourceTypes)
				add(name+" · "+kind, kind, outputs[ref], resources.Content[i], resource)
			}
			if typ == "AWS::SNS::Subscription" && lambdaScalar(lambdaYAMLValue(props, "Protocol")) == "lambda" {
				ref, attribute := lambdaAttributeReference(lambdaYAMLValue(props, "Endpoint"))
				if attribute != "Arn" {
					ref = ""
				}
				add(name+" · SNS", "SNS", outputs[ref], resources.Content[i], resource)
			}
		}
	}
	return triggers, truncated
}

func lambdaEventSource(node *yaml.Node, resources map[string]string) string {
	service := ""
	if node != nil {
		if value := lambdaScalar(node); strings.HasPrefix(value, "arn:") {
			parts := strings.Split(value, ":")
			if len(parts) > 2 {
				service = parts[2]
			}
		} else {
			name, attribute := lambdaAttributeReference(node)
			switch {
			case resources[name] == "AWS::SQS::Queue" && attribute == "Arn":
				service = "sqs"
			case resources[name] == "AWS::Kinesis::Stream" && attribute == "Arn":
				service = "kinesis"
			case resources[name] == "AWS::DynamoDB::Table" && attribute == "StreamArn":
				service = "dynamodb"
			}
		}
	}
	switch service {
	case "sqs":
		return "SQS"
	case "kinesis":
		return "Kinesis"
	case "dynamodb":
		return "DynamoDB"
	default:
		return "EventSourceMapping"
	}
}

func lambdaAttributeReference(node *yaml.Node) (string, string) {
	if node == nil {
		return "", ""
	}
	if node.Tag == "!GetAtt" && node.Kind == yaml.ScalarNode {
		name, attribute, _ := strings.Cut(node.Value, ".")
		return name, attribute
	}
	attr := lambdaYAMLValue(node, "Fn::GetAtt")
	if attr != nil && attr.Kind == yaml.SequenceNode && len(attr.Content) == 2 {
		return lambdaScalar(attr.Content[0]), lambdaScalar(attr.Content[1])
	}
	if attr != nil {
		name, attribute, _ := strings.Cut(lambdaScalar(attr), ".")
		return name, attribute
	}
	return "", ""
}

func lambdaYAMLSource(text string, node *yaml.Node) string {
	if node == nil || node.Line < 1 {
		return ""
	}
	last := node.Line
	var visit func(*yaml.Node)
	visit = func(current *yaml.Node) {
		end := current.Line
		if current.Kind == yaml.ScalarNode && (current.Style == yaml.LiteralStyle || current.Style == yaml.FoldedStyle) {
			end += strings.Count(current.Value, "\n")
		}
		if end > last {
			last = end
		}
		for _, child := range current.Content {
			visit(child)
		}
	}
	visit(node)
	lines := strings.Split(text, "\n")
	if node.Line > len(lines) {
		return ""
	}
	if last > len(lines) {
		last = len(lines)
	}
	result := strings.Join(lines[node.Line-1:last], "\n")
	if len(result) > 8192 {
		result = result[:8192] + "\n[truncated; see retained source]"
	}
	return result
}

func lambdaReference(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Tag == "!Ref" && node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return lambdaScalar(lambdaYAMLValue(node, "Ref"))
}
