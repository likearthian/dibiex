# Graph Report - dbcraft  (2026-11-25)

## Corpus Check
- 159 files · ~895,624 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 1050 nodes · 2030 edges · 28 communities detected
- Extraction: 71% EXTRACTED · 29% INFERRED · 0% AMBIGUOUS · INFERRED: 587 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 21|Community 21]]
- [[_COMMUNITY_Community 22|Community 22]]
- [[_COMMUNITY_Community 24|Community 24]]
- [[_COMMUNITY_Community 27|Community 27]]
- [[_COMMUNITY_Community 28|Community 28]]
- [[_COMMUNITY_Community 31|Community 31]]
- [[_COMMUNITY_Community 34|Community 34]]
- [[_COMMUNITY_Community 36|Community 36]]
- [[_COMMUNITY_Community 37|Community 37]]

## God Nodes (most connected - your core abstractions)
1. `App` - 250 edges
2. `Client` - 56 edges
3. `Column` - 56 edges
4. `Open()` - 38 edges
5. `controlIcon()` - 37 edges
6. `QuoteIdentifier()` - 31 edges
7. `toolbarAction()` - 28 edges
8. `paletteOf()` - 19 edges
9. `Introspector` - 16 edges
10. `cloneSchemaValue()` - 14 edges

## Surprising Connections (you probably didn't know these)
- `TestNativeTableEditorAndBackPreservesDraft()` --calls--> `editablePostgreSQLTable()`  [INFERRED]
  internal\app\alter_table_test.go → internal\app\alter_table.go
- `TestBigQueryProjectNodesRemainIndependent()` --calls--> `paletteOf()`  [INFERRED]
  internal\app\bigquery_projects_test.go → internal\app\style.go
- `TestExecuteToFileShortcutAndMenu()` --calls--> `Open()`  [INFERRED]
  internal\app\query_export_test.go → internal\database\database.go
- `formatCell()` --calls--> `TestConnectionValidationAndQuoting()`  [INFERRED]
  internal\database\bigquery.go → internal\database\connection_test.go
- `Connect()` --calls--> `TestMySQLIntegration()`  [INFERRED]
  internal\database\connection.go → internal\database\mysql_test.go

## Communities

### Community 0 - "Community 0"
Cohesion: 0.02
Nodes (13): App, Run(), findGoogleCloudCLI(), loginGoogleCloudADC(), openGoogleCloudInstaller(), TestGoogleCloudADCLauncher(), cursorOpen(), postgresCursorQuery() (+5 more)

### Community 1 - "Community 1"
Cohesion: 0.04
Nodes (39): editablePostgreSQLTable(), catalogInspection, connectionField(), designerField(), designerInput(), columnTypeField(), databaseIcon(), tableGroup() (+31 more)

### Community 2 - "Community 2"
Cohesion: 0.04
Nodes (41): readPostgreSQLColumn(), readPostgreSQLTableProperties(), cancelBigQueryOnError(), formatCell(), formatCellText(), Client, sqliteColumnList(), sqliteIdentifier() (+33 more)

### Community 3 - "Community 3"
Cohesion: 0.03
Nodes (49): TestCatalogPickerTogglesSchemaVisibility(), TestConnectionTreeHasOneActiveRoot(), TestDeleteUnsavedConnectionMenuLeavesEmptyWorkspace(), TestDeletingConnectionClosesSession(), TestExplorerDoubleClickOpensTableView(), TestSQLiteTableDesignerNativeFlow(), TestSidebarConnectionAndObjectMenus(), paramOccurrence (+41 more)

### Community 4 - "Community 4"
Cohesion: 0.07
Nodes (41): completionContext, completionRelation, completionToken, completionIdentifier(), completionMissingSchema(), completionRelations(), completionReserved(), completionWords() (+33 more)

### Community 5 - "Community 5"
Cohesion: 0.05
Nodes (34): exportPreviewWriter, TestExecuteToFileCapturesSQLWithoutRunningOrCreatingResults(), TestExecuteToFileSelectionAndExistingResultsRemainIndependent(), TestExecuteToFileShortcutAndMenu(), exportCompressions(), exportFilenameStem(), resultExportPreview(), streamResultExport() (+26 more)

### Community 6 - "Community 6"
Cohesion: 0.05
Nodes (37): addBigQueryProjectConfig(), mergeBigQueryCatalog(), removeBigQueryProjectConfig(), removeBigQueryProjectKeys(), TestAdditionalBigQueryProjectsPersistWithoutChangingCredentials(), TestBigQueryProjectNodesRemainIndependent(), TestRemoveBigQueryProjectPersistsAndCanBeReadded(), TestRemoveBigQueryProjectPrunesOnlyItsTreeState() (+29 more)

### Community 7 - "Community 7"
Cohesion: 0.06
Nodes (22): TestBigQueryMultipleProjectCatalog(), simulationMetadataJobs(), TestBigQuerySimulation(), catalogSource, ColumnReference, Definition, TestExplorerCatalogCache(), explorerFixture (+14 more)

### Community 8 - "Community 8"
Cohesion: 0.06
Nodes (27): TestBigQueryProjectDatasetTree(), passwordRetention(), connectionForm, erdNode, TestReferenceExplorerTree(), explorerTableRows(), largeExplorerSchema(), TestBigQueryScopedSearchIsolationAndFlatLimit() (+19 more)

### Community 9 - "Community 9"
Cohesion: 0.06
Nodes (24): TestEditedColumnSelectionKeepsOwner(), TestNativeTableEditorAndBackPreservesDraft(), TestPostgreSQLColumnEditMenuIntegration(), columnInspection, TestAddConnectionWhileAnotherQueryRuns(), TestMultipleConnectionWorkspaces(), workspaceClient(), columnTreeKey() (+16 more)

### Community 10 - "Community 10"
Cohesion: 0.08
Nodes (16): queryNameDialog, queryLibraryPath(), readQueryLibrary(), readSQLFile(), TestQueryLibraryPersistenceAndFiles(), TestReadSQLFile(), validateQueryLibrary(), validQueryName() (+8 more)

### Community 11 - "Community 11"
Cohesion: 0.08
Nodes (18): escapeBigQuery(), schemaReference(), QuoteIdentifierFor(), driverCellValue(), mysqlBinaryType(), mysqlConstraintSQL(), mysqlIndexSQL(), mysqlLiteral() (+10 more)

### Community 12 - "Community 12"
Cohesion: 0.12
Nodes (15): definitionTreeKey(), tableDefinitionItems(), tableDefinitionName(), tableObjectKind(), TestTableObjectsOpenInspector(), columnAnnotation(), definitionAnnotation(), firstSQLGroup() (+7 more)

### Community 13 - "Community 13"
Cohesion: 0.11
Nodes (19): bigQueryColumnDDL(), bigQueryColumns(), bigQueryKind(), bigQueryPartition(), bqIdentifier(), TestNestedSchemaBounds(), propertiesJSON(), propertiesOf() (+11 more)

### Community 14 - "Community 14"
Cohesion: 0.16
Nodes (8): LimitedWriter, PGToolArguments(), RunPostgreSQLTool(), hidePostgreSQLTool(), PGToolOptions, TestPostgreSQLDataEditorAndCSVIntegration(), TestPostgreSQLToolArguments(), toolLog

### Community 15 - "Community 15"
Cohesion: 0.26
Nodes (9): resultColumnIcon(), resultDelimited(), resultRowOrder(), resultSelectedBlock(), resultVisibleOrder(), TestResultCellBlockUsesVisibleColumnOrder(), TestResultFilteringSortingAndCopy(), TestResultPanelControlsAndRetainedState() (+1 more)

### Community 16 - "Community 16"
Cohesion: 0.24
Nodes (5): catalogChoice(), catalogChoices(), catalogBulkChoice(), catalogPicker, TestCatalogPickerShowsDatasetsUnderExpandedProject()

### Community 17 - "Community 17"
Cohesion: 0.47
Nodes (9): assertCursorClosed(), cursorTestSession(), TestPostgreSQLCursorBatchesAndTypes(), TestPostgreSQLCursorBudgetsExpiryAndRerun(), TestPostgreSQLCursorCancellationAndSessionClose(), TestPostgreSQLCursorConnectionsRemainIndependent(), TestPostgreSQLCursorDoesNotDrainMillionRows(), TestPostgreSQLCursorPreservesEditorTransactionAndRejectsScripts() (+1 more)

### Community 18 - "Community 18"
Cohesion: 0.32
Nodes (2): cloneTableDefinition(), tableDesigner

### Community 21 - "Community 21"
Cohesion: 0.5
Nodes (1): TestPostgreSQLCursorEligibility()

### Community 22 - "Community 22"
Cohesion: 0.83
Nodes (3): editDesignerFixture(), TestSharedEditDesignerConfirmsRemovals(), TestSharedEditDesignerTabsAndDraft()

### Community 24 - "Community 24"
Cohesion: 0.67
Nodes (2): failure(), response()

### Community 27 - "Community 27"
Cohesion: 0.67
Nodes (1): Schema

### Community 28 - "Community 28"
Cohesion: 0.67
Nodes (2): ExplorerCatalog, ExplorerGroup

### Community 31 - "Community 31"
Cohesion: 1.0
Nodes (1): queryExecution

### Community 34 - "Community 34"
Cohesion: 1.0
Nodes (1): queryResultTab

### Community 36 - "Community 36"
Cohesion: 1.0
Nodes (1): tableMenuState

### Community 37 - "Community 37"
Cohesion: 1.0
Nodes (1): workspaceTab

## Knowledge Gaps
- **49 isolated node(s):** `catalogPicker`, `completionContext`, `completionRelation`, `sqlSignatureHelp`, `explorerSchemaSearch` (+44 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **Thin community `Community 18`** (8 nodes): `.saveEditedTable()`, `cloneTableDefinition()`, `tableDesigner`, `.addColumn()`, `.originalColumn()`, `.resetEdit()`, `.statements()`, `edit_table_designer.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 21`** (4 nodes): `TestCursorFetchFinishesInOwningTab()`, `TestCursorFetchKeepsSelectedPinnedResultAndGrid()`, `TestPostgreSQLCursorEligibility()`, `cursor_results_test.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 24`** (4 nodes): `failure()`, `requestedRows()`, `response()`, `default.js`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 27`** (3 nodes): `Schema`, `.ID()`, `catalog.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 28`** (3 nodes): `ExplorerCatalog`, `ExplorerGroup`, `explorer_catalog.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 31`** (2 nodes): `queryExecution`, `query_execution.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 34`** (2 nodes): `queryResultTab`, `result_tabs.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 36`** (2 nodes): `tableMenuState`, `table_menu_ui.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 37`** (2 nodes): `workspaceTab`, `workspace_tabs.go`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `App` connect `Community 0` to `Community 1`, `Community 3`, `Community 4`, `Community 5`, `Community 6`, `Community 8`, `Community 9`, `Community 10`, `Community 11`, `Community 12`, `Community 15`, `Community 16`, `Community 18`?**
  _High betweenness centrality (0.515) - this node is a cross-community bridge._
- **Why does `Open()` connect `Community 3` to `Community 2`, `Community 5`, `Community 7`, `Community 9`, `Community 10`, `Community 14`?**
  _High betweenness centrality (0.144) - this node is a cross-community bridge._
- **Why does `QuoteIdentifier()` connect `Community 2` to `Community 3`, `Community 8`, `Community 10`, `Community 11`, `Community 14`?**
  _High betweenness centrality (0.109) - this node is a cross-community bridge._
- **Are the 55 inferred relationships involving `Column` (e.g. with `.columnEditorFields()` and `TestPostgreSQLColumnEditMenuIntegration()`) actually correct?**
  _`Column` has 55 INFERRED edges - model-reasoned connections that need verification._
- **Are the 36 inferred relationships involving `Open()` (e.g. with `TestConnectionTreeHasOneActiveRoot()` and `TestCatalogPickerTogglesSchemaVisibility()`) actually correct?**
  _`Open()` has 36 INFERRED edges - model-reasoned connections that need verification._
- **Are the 35 inferred relationships involving `controlIcon()` (e.g. with `.objectBreadcrumb()` and `.connectionRoots()`) actually correct?**
  _`controlIcon()` has 35 INFERRED edges - model-reasoned connections that need verification._
- **What connects `catalogPicker`, `completionContext`, `completionRelation` to the rest of the system?**
  _49 weakly-connected nodes found - possible documentation gaps or missing edges._