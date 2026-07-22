/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cl

import "golang.org/x/tools/go/ssa"

// CoroIntrinsicCallSiteSemantics keeps focused package tests readable while
// production has only the complete CoroCallSitePlan projection.
func (u *EmissionUniverse) CoroIntrinsicCallSiteSemantics(call ssa.CallInstruction) (CoroIntrinsicCallSemantics, bool, error) {
	return coroIntrinsicCallSiteSemantics(u, call)
}
