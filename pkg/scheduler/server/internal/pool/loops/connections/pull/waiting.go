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

package pull

import "cmp"

// waiting is a min heap of names by dispatches ÷ weight, then first arrival.
type waiting []*flow

func (w *waiting) Len() int { return len(*w) }

func (w *waiting) Less(i, j int) bool {
	a, b := (*w)[i], (*w)[j]
	return cmp.Or(cmp.Compare(a.dispatchesPerWeight, b.dispatchesPerWeight), cmp.Compare(a.seq, b.seq)) < 0
}

func (w *waiting) Swap(i, j int) {
	(*w)[i], (*w)[j] = (*w)[j], (*w)[i]
	(*w)[i].index, (*w)[j].index = i, j
}

func (w *waiting) Push(x any) {
	f := x.(*flow)
	f.index = len(*w)
	*w = append(*w, f)
}

func (w *waiting) Pop() any {
	old := *w
	f := old[len(old)-1]
	old[len(old)-1] = nil
	f.index = -1
	*w = old[:len(old)-1]
	return f
}
