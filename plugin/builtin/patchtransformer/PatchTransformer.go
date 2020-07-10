// Copyright 2019 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

//go:generate pluginator
package main

import (
	"fmt"
	"strings"

	jsonpatch "github.com/evanphx/json-patch"
	"sigs.k8s.io/kustomize/api/filters/patchjson6902"
	"sigs.k8s.io/kustomize/api/filters/patchstrategicmerge"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filtersutil"
	"sigs.k8s.io/yaml"
)

type plugin struct {
	loadedPatch  *resource.Resource
	decodedPatch jsonpatch.Patch
	Path         string          `json:"path,omitempty" yaml:"path,omitempty"`
	Patch        string          `json:"patch,omitempty" yaml:"patch,omitempty"`
	Target       *types.Selector `json:"target,omitempty" yaml:"target,omitempty"`
}

//noinspection GoUnusedGlobalVariable
var KustomizePlugin plugin

func (p *plugin) Config(
	h *resmap.PluginHelpers, c []byte) error {
	err := yaml.Unmarshal(c, p)
	if err != nil {
		return err
	}
	p.Patch = strings.TrimSpace(p.Patch)
	if p.Patch == "" && p.Path == "" {
		return fmt.Errorf(
			"must specify one of patch and path in\n%s", string(c))
	}
	if p.Patch != "" && p.Path != "" {
		return fmt.Errorf(
			"patch and path can't be set at the same time\n%s", string(c))
	}

	if p.Path != "" {
		loaded, loadErr := h.Loader().Load(p.Path)
		if loadErr != nil {
			return loadErr
		}
		p.Patch = string(loaded)
	}

	patchSM, errSM := h.ResmapFactory().RF().FromBytes([]byte(p.Patch))
	patchJson, errJson := jsonPatchFromBytes([]byte(p.Patch))
	if (errSM == nil && errJson == nil) ||
		(patchSM != nil && patchJson != nil) {
		return fmt.Errorf(
			"illegally qualifies as both an SM and JSON patch: [%v]",
			p.Patch)
	}
	if errSM != nil && errJson != nil {
		return fmt.Errorf(
			"unable to parse SM or JSON patch from [%v]", p.Patch)
	}
	if errSM == nil {
		p.loadedPatch = patchSM
	} else {
		p.decodedPatch = patchJson
	}
	return nil
}

func (p *plugin) Transform(m resmap.ResMap) error {
	if p.loadedPatch == nil {
		return p.transformJson6902(m, p.decodedPatch)
	} else {
		// The patch was a strategic merge patch
		return p.transformStrategicMerge(m, p.loadedPatch)
	}
}

// transformStrategicMerge applies the provided strategic merge patch
// to all the resources in the ResMap that match either the Target or
// the identifier of the patch.
func (p *plugin) transformStrategicMerge(m resmap.ResMap, patch *resource.Resource) error {
	if p.Target == nil {
		target, err := m.GetById(patch.OrgId())
		if err != nil {
			return err
		}
		return p.applySMPatch(target, patch)
	}

	resources, err := m.Select(*p.Target)
	if err != nil {
		return err
	}
	for _, res := range resources {
		patchCopy := patch.DeepCopy()
		patchCopy.SetName(res.GetName())
		patchCopy.SetNamespace(res.GetNamespace())
		patchCopy.SetGvk(res.GetGvk())
		err := p.applySMPatch(res, patchCopy)
		if err != nil {
			return err
		}
	}
	return nil
}

// applySMPatch applies the provided strategic merge patch to the
// given resource.
func (p *plugin) applySMPatch(resource, patch *resource.Resource) error {
	node, err := filtersutil.GetRNode(patch)
	if err != nil {
		return err
	}
	return filtersutil.ApplyToJSON(patchstrategicmerge.Filter{
		Patch: node,
	}, resource)
}

// transformJson6902 applies the provided json6902 patch
// to all the resources in the ResMap that match the Target.
func (p *plugin) transformJson6902(m resmap.ResMap, patch jsonpatch.Patch) error {
	if p.Target == nil {
		return fmt.Errorf("must specify a target for patch %s", p.Patch)
	}
	resources, err := m.Select(*p.Target)
	if err != nil {
		return err
	}
	for _, res := range resources {
		err = filtersutil.ApplyToJSON(patchjson6902.Filter{
			Patch: p.Patch,
		}, res)
		if err != nil {
			return err
		}
	}
	return nil
}

// jsonPatchFromBytes loads a Json 6902 patch from
// a bytes input
func jsonPatchFromBytes(
	in []byte) (jsonpatch.Patch, error) {
	ops := string(in)
	if ops == "" {
		return nil, fmt.Errorf("empty json patch operations")
	}

	if ops[0] != '[' {
		jsonOps, err := yaml.YAMLToJSON(in)
		if err != nil {
			return nil, err
		}
		ops = string(jsonOps)
	}
	return jsonpatch.DecodePatch([]byte(ops))
}
