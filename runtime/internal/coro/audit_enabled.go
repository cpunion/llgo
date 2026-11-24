//go:build (!llgo && !coro_no_audit) || coro_audit

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

package coro

// AuditEnabled enables full defensive structural re-validation on hot
// coroutine transitions. Host Go unit tests and -tags=coro_audit builds keep
// it enabled so every malformed-state contract remains continuously verified;
// default LLGo builds omit redundant structural re-audits while keeping all
// semantic ownership, generation, cancellation, and rollback checks.
const AuditEnabled = true
