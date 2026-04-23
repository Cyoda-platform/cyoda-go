## Cyoda Cloud numeric type classification algorithm

This document describes the numeric classification path used during entity-data ingestion, with code pointers to the implementation that currently drives the behavior.

## 1. Class inventory

| Class/module | Package / path | Role | Owns vs delegates |
|---|---|---|---|
| `org.cyoda.entity.parsing.JacksonParser` | `client/src/main/kotlin/org/cyoda/entity/parsing/JacksonParser.kt` | Main ingestion-time classifier and recorder for parsed scalar leaves. | Owns tree traversal, path handling, scope enforcement, model-aware coercion, and writing into `ValueMaps`; delegates raw scalar classification to `JsonNode.typeValue()` and conversions/helpers. |
| `org.cyoda.entity.parsing.ParsingSpec` | `client/src/main/kotlin/org/cyoda/entity/parsing/ParsingSpec.kt` | Runtime policy object for parsing behavior. | Owns defaults for `intScope`, `decimalScope`, `parseStrings`, format, and path settings; read by `JacksonParser`. |
| `org.cyoda.entity.model.DataType` | `client/src/main/kotlin/org/cyoda/entity/model/DataType.kt` | Numeric type system plus widening lattice and string-to-type parsers. | Owns the enum, `wideningConversionMap`, `findCommonDataType`, `parseStringOrNull`, and assignability checks. |
| `org.cyoda.entity.model.DataTypeValue<T>` | `client/src/main/kotlin/org/cyoda/entity/model/DataTypeValue.kt` | Pair of classified type plus typed value. | Owns the result container returned by low-level classifiers; used by `JacksonParser` and coercion code. |
| `JsonNode.typeValue()` and related helpers | `client/src/main/kotlin/org/cyoda/entity/parsing/ParserFunctions.kt` | Raw scalar classifier from Jackson node kind into `DataTypeValue`. | Owns integer/decimal family decision and raw subtype decision; delegates range predicates to `isInt128`, `isFloat`, `isDouble`, `isInt128Decimal`. |
| `String.parseWholeNumberOrNull`, `parseBigIntegerOrNull`, `parseToFloatOrNull`, `parseToDoubleOrNull` | `client/src/main/kotlin/org/cyoda/entity/parsing/NumberParsing.kt` | String-to-number parsing helpers used for coercion and explicit `DataType.parseString`. | Owns flexible numeric string parsing, including scientific notation and whole-number validation. |
| `Number.toBigDecimalOrNull`, `toByteOrNull`, `toShortOrNull`, `toIntOrNull`, `toLongOrNull`, `toBigIntegerOrNull`, `convertToWholeNumberOrNull` | `client/src/main/kotlin/org/cyoda/entity/parsing/WholeNumberConversions.kt` | Lossless integer conversion helpers. | Owns exact whole-number conversion and range checks for scope promotion and coercion. |
| `Number.toFloatOrNull`, `toDoubleOrNull`, `toBigDecimalWithInt128ConstraintOrNull`, `toUnboundDecimalOrNull`, `convertToDecimalNumberOrNull` | `client/src/main/kotlin/org/cyoda/entity/parsing/DecimalNumberConversions.kt` | Lossless-enough decimal conversion helpers for scope promotion. | Owns finite/underflow checks and decimal conversions for `FLOAT`/`DOUBLE` scope enforcement. |
| `String.dataTypeValueFromValue()` | `client/src/main/kotlin/org/cyoda/entity/model/ValueDetectionFunctions.kt` | String autodiscovery helper. | Owns non-numeric string discovery; explicitly does **not** infer numeric types from JSON strings. |
| `org.cyoda.entity.model.Polymorphic` | `client/src/main/kotlin/org/cyoda/entity/model/Polymorphic.kt` | Ordered compatible type-set abstraction for inferred/model field types. | Owns compatible-set construction, merging, and collapse-to-common-type logic. |
| `org.cyoda.entity.model.ComparableDataType` | `client/src/main/kotlin/org/cyoda/entity/model/ComparableDataType.kt` | Sort order and incompatibility metadata for `Polymorphic`. | Owns ordering by generality using `DataType.wideningConversionMap`; delegates numeric reachability to `DataType`. |
| `org.cyoda.entity.model.ValueMaps` | `client/src/main/kotlin/org/cyoda/entity/model/ValueMaps.kt` | Storage for per-path typed values and type references. | Owns the concrete maps (`ints`, `longs`, `bigIntegers`, `doubles`, etc.) and `typeReferences`. |
| `org.cyoda.entity.model.structure.items.ModelDataType` | `client/src/main/kotlin/org/cyoda/entity/model/structure/items/ModelDataType.kt` | Field-type wrapper used when inferred models merge observations. | Owns set union of observed types and conversion to `Polymorphic` / homomorphic representative. |
| `org.cyoda.entity.model.structure.EntityStructureModel` | `client/src/main/kotlin/org/cyoda/entity/model/structure/EntityStructureModel.kt` | Cross-entity structure/model merge entry point. | Owns node-level model merging and extraction of polymorphic field views; delegates field-type merge to node/field types. |
| `com.cyoda.tdb.logic.parsing.JsonParserService` | `tree-node/tree-node-backend/src/main/kotlin/com/cyoda/tdb/logic/parsing/JsonParserService.kt` | Tree-node backend adapter that instantiates `JacksonParser`. | Owns mapper selection by import format and parser construction; delegates actual classification to `JacksonParser`. |

## 2. Relationships

### Static structure

- `JsonParserService.ParsingSpec.createParser()` constructs `JacksonParser` with the chosen mapper and optional `EntityStructureModel` (`tree-node/.../JsonParserService.kt:30-38`).
- `JacksonParser` reads policy from `ParsingSpec` (`client/.../JacksonParser.kt:47-67`).
- `JacksonParser` emits `DataTypeValue` and writes the results into `ValueMaps.typeReferences` plus the matching typed map (`client/.../JacksonParser.kt:320-329`, `client/.../ValueMaps.kt:24-79`).
- `Polymorphic` stores ordered `ComparableDataType` instances in a `TreeSet` (`client/.../Polymorphic.kt:15-18`).
- `ComparableDataType.compareTo()` depends on `DataType.wideningConversionMap` (`client/.../ComparableDataType.kt:56-68`).
- `ModelDataType` wraps one or more `DataType` values and exposes either a single type or a `Polymorphic` view (`client/.../structure/items/ModelDataType.kt:49-60`).

### Runtime-configurable relationships

- `JacksonParser` switches behavior with `ParsingSpec.intScope`, `decimalScope`, `parseStrings`, `importFormat`, and the optional `structureModel` (`client/.../JacksonParser.kt:53-67`; `client/.../ParsingSpec.kt:25-40`).
- If a `structureModel` exists, `JacksonParser.handleDefault()` stops doing free classification and instead tries to coerce the incoming value into the field's allowed `Polymorphic` types (`client/.../JacksonParser.kt:291-320`).
- `allowedChanges` from the structure model decides whether incompatible new paths/types raise exceptions or extend the model (`client/.../JacksonParser.kt:53-56`, `310-316`).

### Call graph for one scalar value

Free discovery path:

1. `JsonParserService.createParser()` → `JacksonParser` (`tree-node/.../JsonParserService.kt:33-38`)
2. `JacksonParser.parse(str)` reads a `JsonNode` tree (`client/.../JacksonParser.kt:109-121`)
3. `processObjectFields()` / `processArrayElements()` reaches a scalar leaf (`client/.../JacksonParser.kt:190-250`)
4. `handleDefault()` calls `value.parseLeaf()` if no model controls the field (`client/.../JacksonParser.kt:285-320`)
5. `parseLeaf()` calls `JsonNode.typeValue(parseStrings)` (`client/.../JacksonParser.kt:333-349`)
6. `typeValue()` chooses the raw `DataTypeValue` based on Jackson node kind and BigDecimal/BigInteger predicates (`client/.../ParserFunctions.kt:118-170`)
7. `parseLeaf()` optionally upgrades raw small integer / float results to the configured minimum scopes (`client/.../JacksonParser.kt:336-392`)
8. `handleDefault()` records the final `DataType` in `typeReferences` and stores the typed value in the matching map (`client/.../JacksonParser.kt:322-329`)

Model-constrained path:

1. Same traversal
2. `handleDefault()` looks up the field's `Polymorphic` type set from the structure model (`client/.../JacksonParser.kt:291-309`)
3. `JsonNode.coerceOrNull(possibleTypes)` first checks exact raw-node type, then reparses `asText()` through each allowed `DataType.parseStringOrNull()` (`client/.../ParserFunctions.kt:75-112`; `client/.../parsing/LeafFieldParser.kt:44-45`; `client/.../DataType.kt:125-166`)
4. If coercion fails and type extension is disallowed, `FoundIncompatibleTypeWitEntityModelException` gets thrown (`client/.../JacksonParser.kt:313-316`)

## 3. The classification algorithm, end to end

### 3.1 Raw inputs and parser setup

- `JacksonParser.defaultObjectMapper()` enables `USE_BIG_DECIMAL_FOR_FLOATS` but does **not** enable `USE_BIG_INTEGER_FOR_INTS` (`client/.../JacksonParser.kt:454-463`).
- Result: JSON floating literals arrive as `DecimalNode`/`BigDecimal`, while integral literals still arrive as Jackson-sized integer nodes (`IntNode`, `LongNode`, `BigIntegerNode`).
- A direct runtime check on the current classpath confirmed:
  - `2^53` → `LongNode`
  - `2^53 + 1` → `LongNode`
  - `2^63 - 1` → `LongNode`
  - `2^63` → `BigIntegerNode`

### 3.2 Raw discriminator: integer family vs decimal family

`JsonNode.typeValue()` uses the Jackson node's runtime kind, not source-text syntax, as the first discriminator (`client/.../ParserFunctions.kt:118-170`):

- `isBigDecimal` → decimal branch
- `isBigInteger` → large integer branch
- `isInt` / `isLong` / `isShort` / `isIntegralNumber` → integer branch
- `isTextual` → string autodiscovery, which intentionally excludes numerics (`client/.../ValueDetectionFunctions.kt:71-77`)

So the main ingestion path does **not** determine integer-vs-decimal by scanning for a decimal point or exponent itself; Jackson already chose the node category.

### 3.3 Raw integer branch

Raw integer classification in `typeValue()` works like this (`client/.../ParserFunctions.kt:133-155`):

1. `BigIntegerNode`
   - if `value.isInt128()` → `BIG_INTEGER`
   - else → `UNBOUND_INTEGER`
2. `IntNode`
   - `Byte.MIN_VALUE..Byte.MAX_VALUE` → `BYTE`
   - `Short.MIN_VALUE..Short.MAX_VALUE` → `SHORT`
   - otherwise → `INTEGER`
3. `LongNode` → `LONG`
4. Fallback `isIntegralNumber` → `LONG`

The `Int128` limits come from `INT128_MIN_VALUE = -2^127` and `INT128_MAX_VALUE = 2^127 - 1` (`client/.../ParserFunctions.kt:28-31`, `49`).

### 3.4 Raw decimal branch

Raw decimal classification in `typeValue()` works like this (`client/.../ParserFunctions.kt:121-130`):

1. Convert Jackson decimal node to `BigDecimal` and call `stripTrailingZeros()`.
2. Compute `precision = value.precision()` and `scale = value.scale()`.
3. Decide in this exact order:
   - if `isFloat(precision, scale)` → `FLOAT`
   - else if `isDouble(precision, scale)` → `DOUBLE`
   - else if `isInt128Decimal(precision, scale)` → `BIG_DECIMAL`
   - else → `UNBOUND_DECIMAL`

The predicates use decimal digit/scale envelopes, not IEEE round-trip comparison (`client/.../ParserFunctions.kt:33-59`):

- `FLOAT`: `precision <= 6` and `scale in -31..31`
- `DOUBLE`: `precision <= 15` and `scale in -292..292`
- `BIG_DECIMAL`: `scale <= 18` and Trino/Int128 fit rules

`BIG_DECIMAL` uses Trino's fixed-scale Int128 contract, documented inline in `DataType` and implemented by `isInt128Decimal()` (`client/.../DataType.kt:39-63`; `client/.../ParserFunctions.kt:54-59`):

- definite fit if `precision <= 38` and exponent (`precision - scale`) `<= 20`
- possible fit if `precision <= 39` and exponent `<= 21`, then `setScale(18).unscaledValue().isInt128()` must succeed
- otherwise the value lands in `UNBOUND_DECIMAL`

### 3.5 Post-classification scope enforcement

`JacksonParser.parseLeaf()` applies a **minimum resolution** policy after raw classification (`client/.../JacksonParser.kt:333-392`):

- raw `BYTE`/`SHORT`/`INTEGER` get re-resolved upward according to `intScope`
- raw `FLOAT` gets re-resolved upward according to `decimalScope`
- raw `LONG`, `BIG_INTEGER`, `UNBOUND_INTEGER`, `DOUBLE`, `BIG_DECIMAL`, `UNBOUND_DECIMAL` stay as-is

Default policy from `ParsingSpec` (`client/.../ParsingSpec.kt:36-39`):

- `intScope = INTEGER`
- `decimalScope = DOUBLE`

Therefore, under **default ingestion**:

- values that raw-classify as `BYTE` or `SHORT` become `INTEGER`
- values that raw-classify as `FLOAT` become `DOUBLE`
- `LONG` and above stay unchanged
- `DOUBLE` and above stay unchanged

The promotion code uses exact conversion helpers, not unsafe casts (`client/.../JacksonParser.kt:352-392`; `client/.../WholeNumberConversions.kt:17-117`; `client/.../DecimalNumberConversions.kt:18-119`).

### 3.6 Recording the result

After classification/coercion, `handleDefault()` does two writes (`client/.../JacksonParser.kt:322-329`):

- `current.payload.value.typeReferences[newPath] = valueType`
- inserts the typed value into the matching `ValueMaps` bucket (`ints`, `longs`, `doubles`, `bigIntegers`, etc.)

The bucket layout lives in `ValueMaps` (`client/.../ValueMaps.kt:24-108`).

### 3.7 Pseudocode

```text
raw = JsonNode.typeValue(parseStrings)

if no structure model:
    if raw.type in {BYTE, SHORT, INTEGER}:
        final = promote_integer_to_min_scope(raw, intScope)
    else if raw.type == FLOAT:
        final = promote_decimal_to_min_scope(raw, decimalScope)
    else:
        final = raw
else:
    final = coerce_to_model_polymorphic_types(raw node, possibleTypes)
    if final == null and type extension forbidden:
        throw FoundIncompatibleTypeWitEntityModelException

typeReferences[path] = final.type
typedBucket(final.type)[relativePath] = final.value
```

## 4. Widening and merge across values

### 4.1 Raw numeric widening lattice

`DataType.wideningConversionMap` defines the numeric reachability graph (`client/.../DataType.kt:239-272`):

- `BYTE -> {SHORT, INTEGER, LONG, FLOAT, DOUBLE, BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL}`
- `SHORT -> {INTEGER, LONG, FLOAT, DOUBLE, BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL}`
- `INTEGER -> {LONG, DOUBLE, BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL}`
- `LONG -> {BIG_INTEGER, BIG_DECIMAL, UNBOUND_INTEGER, UNBOUND_DECIMAL}`
- `FLOAT -> {DOUBLE, BIG_DECIMAL, UNBOUND_DECIMAL}`
- `DOUBLE -> {UNBOUND_DECIMAL}`
- `BIG_INTEGER -> {UNBOUND_INTEGER, UNBOUND_DECIMAL}`
- `UNBOUND_INTEGER -> {UNBOUND_DECIMAL}`
- `BIG_DECIMAL -> {UNBOUND_DECIMAL}`
- `UNBOUND_DECIMAL -> {}`

Two inline comments explain important asymmetries (`client/.../DataType.kt:253-268`):

- `INTEGER` does **not** widen to `FLOAT` because float precision is too low.
- `LONG` does **not** widen to `DOUBLE` because double precision is too low.

### 4.2 Type-set evolution inside inferred models

- `ModelDataType.merge()` unions observed type sets, except that `NULL` disappears when merged with a concrete type (`client/.../structure/items/ModelDataType.kt:54-60`).
- `Polymorphic` stores the resulting compatible set in sorted order (`client/.../Polymorphic.kt:17-39`; `client/.../ComparableDataType.kt:56-68`).
- `Polymorphic.findCommonDataType()` chooses the last sorted type if all members can assign into it; otherwise it searches `invertedConversions` for a common supertype and falls back to `STRING` (`client/.../Polymorphic.kt:51-63`; `client/.../DataType.kt:288-309`).

### 4.3 Existing-model ingestion behavior

With an existing structure model, Cyoda does not freely re-infer the field; it tries to coerce into the model's allowed `Polymorphic` types (`client/.../JacksonParser.kt:291-320`).

- If the incoming value already matches one allowed type, that type wins (`client/.../ParserFunctions.kt:81-87`).
- Otherwise, Cyoda reparses `asText()` through the allowed types in order (`client/.../ParserFunctions.kt:88-112`; `client/.../parsing/LeafFieldParser.kt:44-45`).
- If coercion fails and type extension is forbidden, ingestion rejects with `FoundIncompatibleTypeWitEntityModelException` (`client/.../JacksonParser.kt:313-316`).

This produces an observed asymmetry verified by integration tests (`integration-tests/.../EntityInteractorIT.kt:843-875`):

- existing decimal field, incoming integer → accepted (`13.111` then `13`)
- existing integer field, incoming decimal → rejected (`13` then `13.111`)

### 4.4 Narrower value than current known type

If the model already allows a wider numeric type, the narrower observation gets coerced into the wider type rather than shrinking the schema. That is exactly what the `compatible types` integration test demonstrates (`integration-tests/.../EntityInteractorIT.kt:844-856`).

## 5. Configuration and policy

| Knob | Type | Default | Code | Read by | Effect |
|---|---|---|---|---|---|
| `intScope` | `DataType` | `INTEGER` | `client/.../ParsingSpec.kt:36-39` | `JacksonParser.parseLeaf()` | Minimum freely inferred integer resolution. Valid path in code: `BYTE -> SHORT -> INTEGER -> LONG` (`client/.../JacksonParser.kt:352-373`). |
| `decimalScope` | `DataType` | `DOUBLE` | `client/.../ParsingSpec.kt:38-39` | `JacksonParser.parseLeaf()` | Minimum freely inferred decimal resolution. Valid path in code: `FLOAT -> DOUBLE` (`client/.../JacksonParser.kt:375-392`). |
| `parseStrings` | `Boolean` | `true` | `client/.../ParsingSpec.kt:27-29` | `JacksonParser`, `JsonNode.typeValue()` | Controls whether textual nodes undergo autodiscovery. When no model exists, `JacksonParser` forces it on (`client/.../JacksonParser.kt:60-62`). |
| `importFormat` | `ImportFormat` | `JSON` | `client/.../ParsingSpec.kt:31-34` | `JsonParserService.createParser()` | Chooses JSON vs XML mapper (`tree-node/.../JsonParserService.kt:30-38`). |
| `alsoSaveInStrings` | `Boolean` | `false` | `client/.../ParsingSpec.kt:26-28` | `JacksonParser.handleDefault()` | If a textual node classifies as a non-`STRING` discoverable type, also stores the original text under `STRING` (`client/.../JacksonParser.kt:328-329`). |
| `structureModel.allowedChanges` | `ChangeLevel?` | none in `ParsingSpec`; comes from model | `client/.../JacksonParser.kt:53-56` | `JacksonParser.handleDefault()` | Decides whether new types/paths extend the model or raise incompatibility exceptions. |
| Float precision envelope | constants | `precision=6`, `scale<=31` | `client/.../ParserFunctions.kt:33-36`, `50-53` | `typeValue()`, string/number decimal helpers | Raw `FLOAT` decision. |
| Double precision envelope | constants | `precision=15`, `scale<=292` | `client/.../ParserFunctions.kt:35-36`, `52-53` | `typeValue()`, string/number decimal helpers | Raw `DOUBLE` decision. |
| Trino decimal envelope | constants | `scale<=18`, strict/loose precision-exponent gates | `client/.../ParserFunctions.kt:38-59` | `typeValue()`, `DataType.parseStringOrNull(BIG_DECIMAL)` | Distinguishes `BIG_DECIMAL` from `UNBOUND_DECIMAL`. |

Per-field overrides exist only through the `EntityStructureModel`: once a field already has a `Polymorphic` type set, free classification yields to coercion into that set (`client/.../JacksonParser.kt:299-320`). Multi-type arrays can even override by array index (`client/.../JacksonParser.kt:300-308`).

## 6. Edge cases with classifications

The table below uses **default ingestion** (`ParsingSpec()`), not the lower-level raw `typeValue()` helper.

| Input form | Default result | Why |
|---|---|---|
| numeric `0` | `INTEGER` | Raw `BYTE`, then promoted by default `intScope=INTEGER` (`client/.../ParserFunctions.kt:141-147`; `client/.../JacksonParser.kt:352-369`). Runtime-verified. |
| numeric `-1` | `INTEGER` | Same as above. Runtime-verified. |
| numeric `127`, `128`, `-128`, `-129` | all `INTEGER` | Raw results differ (`BYTE`/`SHORT`), but default scope promotes them to `INTEGER`. Runtime-verified. |
| numeric at short/int boundaries within `IntNode` range | `INTEGER` | Same default-scope promotion. Raw helper only preserves `SHORT` if `intScope=SHORT` or `BYTE`. |
| numeric `2^53` | `LONG` | Jackson creates `LongNode`; free parser keeps `LONG`. Runtime-verified. |
| numeric `2^53 + 1` | `LONG` | Same. Runtime-verified. |
| numeric `2^63 - 1` | `LONG` | Jackson `LongNode`; runtime-verified. |
| numeric `2^63` | `BIG_INTEGER` | Jackson `BigIntegerNode`, and value fits Int128 (`client/.../ParserFunctions.kt:133-138`). Runtime-verified. |
| integer with 30+ digits | `BIG_INTEGER` if within Int128, else `UNBOUND_INTEGER` | Boundary enforced by `isInt128()`. 30+ digits usually exceed Int128 and land in `UNBOUND_INTEGER` (`client/.../ParserFunctions.kt:49`, `133-138`; `client/.../ParserFunctionsKtTest.kt:331-355`). |
| numeric `0.0` | `DOUBLE` | Raw decimal becomes `FLOAT` after `stripTrailingZeros`, then default `decimalScope=DOUBLE` upgrades it. Runtime-verified. |
| numeric `-0.0` | `DOUBLE` | Same; sign does not survive classification as a distinct type. Runtime-verified. |
| numeric `0` | `INTEGER`, not decimal | Integral Jackson node, so it goes through integer branch. Runtime-verified. |
| numeric `0.1` | `DOUBLE` | Raw `FLOAT` by precision/scale envelope, then default scope upgrades to `DOUBLE`. Runtime-verified. |
| numeric `1.5` | `DOUBLE` | Same. Runtime-verified. |
| decimal with 20 significant digits | at least `BIG_DECIMAL`, often `UNBOUND_DECIMAL` if Trino bounds fail | `DOUBLE` stops at precision 15; `BIG_DECIMAL` uses Trino Int128 + scale 18 (`client/.../ParserFunctions.kt:33-59`). |
| numeric `1.00` | `DOUBLE` | Trailing zeros stripped before classification; raw `FLOAT` then upgraded to `DOUBLE`. Runtime-verified. |
| numeric `1.5e2` | `DOUBLE` | Jackson supplies `BigDecimal`; raw `FLOAT`/`DOUBLE` decided by precision/scale, not exponent token text. Runtime-verified. |
| numeric `NaN` / `Infinity` / `-Infinity` as strings | `STRING` in free discovery; `IllegalArgumentException` if parsed explicitly as numeric type | String autodiscovery excludes numbers (`client/.../ValueDetectionFunctions.kt:71-77`). Explicit numeric string parsing rejects them (`client/.../DataTypeParsingTest.kt:113-143`, `293-307`). |
| numeric underflow like `1e-400` | `UNBOUND_DECIMAL` | Too much scale for `DOUBLE`, too much scale for `BIG_DECIMAL`. Runtime-verified. |
| numeric overflow like `1e400` | `UNBOUND_DECIMAL` | Exceeds `DOUBLE` envelope and Trino decimal envelope. Runtime-verified. |
| string `"42"` | `STRING` | Numeric strings intentionally stay strings in discovery mode (`client/.../ValueDetectionFunctions.kt:71-77`, `119-120`; `client/.../JacksonParserTest.kt:773-775`). Runtime-verified. |
| `null` | `NULL` | `typeValue()` returns `DataType.NULL` (`client/.../ParserFunctions.kt:159`). Runtime-verified. |
| boolean `true` / `false` | `BOOLEAN` | `typeValue()` returns `BOOLEAN` (`client/.../ParserFunctions.kt:157-159`). Runtime-verified. |
| empty array `[]` | no leaf numeric type; no type reference recorded at that path | `handleArray()` returns immediately when `size()==0` (`client/.../JacksonParser.kt:420-446`). Runtime-verified. |
| empty object `{}` | `OBJECT` | `handleObject()` records `DataType.OBJECT` even if it has no fields (`client/.../JacksonParser.kt:395-417`). Runtime-verified. |

## 7. Test cases

| File | What it verifies | Representative cases |
|---|---|---|
| `client/src/test/kotlin/org/cyoda/entity/parsing/ParserFunctionsKtTest.kt` | Raw `JsonNode.typeValue()` classification for numeric nodes. | `DecimalNode("123.456") -> FLOAT`, `DOUBLE_MAX_VALUE -> DOUBLE`, Trino-bound decimal -> `BIG_DECIMAL`, huge decimal -> `UNBOUND_DECIMAL`, Int128 overflow -> `UNBOUND_INTEGER` (`.../ParserFunctionsKtTest.kt:182-355`). |
| `client/src/test/kotlin/org/cyoda/entity/model/DataTypeParsingTest.kt` | `DataType.parseString` behavior for numeric strings. | Scientific notation for whole numbers, type bounds, invalid numeric strings, float/double precision boundaries, `BIG_DECIMAL` scale limit (`.../DataTypeParsingTest.kt:27-416`). |
| `client/src/test/kotlin/org/cyoda/entity/parsing/JacksonParserTest.kt` | End-to-end parser classification and recording into `ValueMaps`. | Mixed JSON document with `byte`, `short`, `integer`, `long`, `big-integer`, `float`, `double`, `big-decimal`, numeric string staying `STRING` (`.../JacksonParserTest.kt:738-790`). |
| `client/src/test/kotlin/org/cyoda/entity/model/DataTypeTest.kt` | Collapse of widening sets to common type. | Iterates all widening-map-derived compatible sets and asserts `findCommonDataType()` matches the most general sorted member (`.../DataTypeTest.kt:275-304`). |
| `client/src/test/kotlin/org/cyoda/entity/parsing/DecimalNumberConversionsKtTest.kt` | Decimal conversion helpers and unconstrained decimal conversion. | `toUnboundDecimalOrNull()` for `Int`, `Float`, `Double`, `BigDecimal`; conversion correctness (`.../DecimalNumberConversionsKtTest.kt:269-288`). |
| `tree-node/tree-node-backend/src/test/kotlin/com/cyoda/tdb/search/polymorphic/PolymorphicNumberConversionsKtTest.kt` | Numeric polymorphic query-condition conversion and float/double boundaries on the search side. | Integer + float type sets, rounding for range operations, custom boundary constants (`.../PolymorphicNumberConversionsKtTest.kt:15-86`). |
| `tree-node/tree-node-backend/src/test/kotlin/com/cyoda/tdb/logic/parsing/JsonParserServiceTest.kt` | Backend service wrapper around `JacksonParser`. | Parsing nested JSON with `ParsingSpec(intScope = BYTE, decimalScope = FLOAT)` and verifying numeric leaf storage (`.../JsonParserServiceTest.kt:19-55`). |
| `integration-tests/src/test/kotlin/net/cyoda/saas/entity/EntityInteractorIT.kt` | Schema-lock compatibility behavior across entities. | Decimal field then integer succeeds; integer field then decimal throws `FoundIncompatibleTypeWitEntityModelException` (`.../EntityInteractorIT.kt:843-875`). |

There is no dedicated round-trip test class just for numeric inference in the scanned sources, but the combination of `DataType.parseString*`, `typeValue()`, and parser tests covers both raw-node and text-reparse paths.

## 8. Design rationale

The code comments expose several rationale points:

- **Numeric strings stay strings on purpose.** `ValueDetectionFunctions.kt` explicitly says parsing numeric-looking strings as numbers would mislead inference because JSON already has a native number type, and because splitting numeric strings across byte/short/int/float/double maps would scatter data (`client/.../ValueDetectionFunctions.kt:71-77`).
- **`BIG_INTEGER` and `BIG_DECIMAL` exist for Trino compatibility.** `DataType.kt` documents both as Int128-bounded categories; anything beyond those bounds becomes `UNBOUND_*` and downstream Trino must treat it as a string (`client/.../DataType.kt:39-63`).
- **The widening lattice rejects some seemingly obvious promotions to protect precision.** Comments in `wideningConversionMap` say `INTEGER -> FLOAT` and `LONG -> DOUBLE` are intentionally omitted because precision is too low (`client/.../DataType.kt:253-268`).
- **The decimal path uses decimal-digit envelopes, not binary exactness.** `isFloat()` / `isDouble()` rely on `precision()` and `scale()` only (`client/.../ParserFunctions.kt:50-53`). That design favors a cheap predicate over round-trip analysis.

Performance-wise, the hot path keeps work bounded:

- one Jackson parse to a tree
- one branch on node kind
- one `stripTrailingZeros()` plus `precision()` / `scale()` for decimal nodes
- primitive range checks for small integers
- at most a short recursive climb for `intScope` / `decimalScope`

The code does **not** do repeated parse-string / round-trip / ULP analysis in the free-discovery path.

## 9. Known deviations from intuition

1. **Default ingestion does not preserve tiny integer/float buckets.** Because defaults are `intScope=INTEGER` and `decimalScope=DOUBLE`, free ingestion usually produces `INTEGER`, `LONG`, `BIG_INTEGER`, `UNBOUND_INTEGER`, `DOUBLE`, `BIG_DECIMAL`, or `UNBOUND_DECIMAL` rather than `BYTE`, `SHORT`, or `FLOAT` (`client/.../ParsingSpec.kt:36-39`; `client/.../JacksonParser.kt:333-392`).
2. **`0.1` can classify as floating-point even though binary float/double cannot represent it exactly.** The raw classifier uses decimal precision/scale envelopes, not exact IEEE representability (`client/.../ParserFunctions.kt:50-53`, `121-130`).
3. **`Float.MAX_VALUE.toString()` does not necessarily classify as `FLOAT`.** A test shows it classifies as `DOUBLE` because its decimal presentation exceeds the `FLOAT` precision/scale envelope (`client/.../ParserFunctionsKtTest.kt:217-223`).
4. **Numeric strings do not become numeric fields during discovery.** `"42"` remains `STRING` unless an existing model later coerces it into a numeric type (`client/.../ValueDetectionFunctions.kt:71-77`, `119-120`; `client/.../ParserFunctions.kt:75-112`).
5. **Empty arrays disappear from `typeReferences`.** The parser records nothing at that path when an array has zero elements (`client/.../JacksonParser.kt:432-433`).
6. **Compatibility across entities is asymmetric.** Integer data can fit into an existing decimal field, but decimal data does not fit into an existing integer field once the model is locked (`integration-tests/.../EntityInteractorIT.kt:843-875`).
7. **`BIG_DECIMAL` is not “any BigDecimal”.** It is the Trino-compatible subset only; otherwise Cyoda moves to `UNBOUND_DECIMAL` even though the in-memory value still uses `BigDecimal` (`client/.../DataType.kt:39-50`; `client/.../ParserFunctions.kt:54-59`, `121-130`).

## Appendix: central code pointers

- Raw numeric classifier: `client/src/main/kotlin/org/cyoda/entity/parsing/ParserFunctions.kt:118-170`
- Scope promotion: `client/src/main/kotlin/org/cyoda/entity/parsing/JacksonParser.kt:333-392`
- Numeric string parsers: `client/src/main/kotlin/org/cyoda/entity/parsing/NumberParsing.kt:29-73`
- Whole-number conversions: `client/src/main/kotlin/org/cyoda/entity/parsing/WholeNumberConversions.kt:17-117`
- Decimal conversions: `client/src/main/kotlin/org/cyoda/entity/parsing/DecimalNumberConversions.kt:18-119`
- Widening lattice: `client/src/main/kotlin/org/cyoda/entity/model/DataType.kt:239-309`
- Polymorphic merge/collapse: `client/src/main/kotlin/org/cyoda/entity/model/Polymorphic.kt:15-63`
