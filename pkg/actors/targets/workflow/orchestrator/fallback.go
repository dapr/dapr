/*
Copyright 2026 The Dapr Authors
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

package orchestrator

// isFallback reports whether this daprd hosts the workflow actor type as a
// fallback host (Options.Fallback): it has no local executor, so a turn here
// fails at once and the instance advances only once its actor moves to a
// daprd with one. It changes while the factory is registered, as a daprd
// turns into a fallback host or back in place, so it is read where it is
// needed rather than once.
func (f *factory) isFallback() bool {
	return f.fallback != nil && f.fallback()
}

// localFastPath reports whether an operation served outside a turn may take
// the WorkflowsFastPath local drives and elisions. A fallback host takes the
// durable reminder path instead: a local drive runs a turn here, which fails,
// and an elided reminder would leave the operation to a janitor no turn has
// armed yet. Decisions made inside a turn keep using fastPath: a turn runs
// only on a daprd with a local executor.
func (f *factory) localFastPath() bool {
	return f.fastPath && !f.isFallback()
}
