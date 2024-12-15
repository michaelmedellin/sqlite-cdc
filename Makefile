.PHONY: test test/lint test/unit test/coverage
.PHONY: tools tools/update
.PHONY: generate fmt clean clean/test clean/tools

PROJECT_PATH = $(shell pwd -L)
GOFLAGS ::= ${GOFLAGS}
GOTOOLS = $(shell grep '_' $(TOOLS_DIR)/tools.go | sed 's/[[:space:]]*_//g' | sed 's/\"//g')
BUILD_DIR = $(PROJECT_PATH)/.build
TOOLS_DIR = $(PROJECT_PATH)/internal/tools
TOOLS_FILE = $(TOOLS_DIR)/tools.go
DIST_DIR = $(PROJECT_PATH)/dist
BIN_DIR = $(PROJECT_PATH)/.bin
BENCH_DIR = $(BUILD_DIR)/.bench
COVER_DIR = $(BUILD_DIR)/.coverage
COVERAGE_UNIT = $(COVER_DIR)/unit.out
COVERAGE_UNIT_INTERCHANGE = $(COVERAGE_UNIT:.out=.interchange)
COVERATE_UNIT_HTML = $(COVERAGE_UNIT:.out=.html)
COVERAGE_UNIT_XML = $(COVERAGE_UNIT:.out=.xml)
COVERAGE_COMBINED = $(COVER_DIR)/combined.out
COVERAGE_COMBINED_INTERCHANGE = $(COVERAGE_COMBINED:.out=.interchange)
COVERAGE_COMBINED_HTML = $(COVERAGE_COMBINED:.out=.html)
COVERAGE_COMBINED_XML = $(COVERAGE_COMBINED:.out=.xml)
GOIMPORT_LOCAL = github.com/kevinconway
GOLANGCILINT_CONFIG = $(PROJECT_PATH)/.golangci.yaml
GOCMD = GOFLAGS=$(GOFLAGS) go
BUILD_MODE = local
BUILD_FLAGS = --clean
ifneq ($(BUILD_MODE),tag)
	BUILD_FLAGS = --clean --snapshot
endif

#######
# https://stackoverflow.com/a/10858332
check_defined = \
    $(strip $(foreach 1,$1, \
        $(call __check_defined,$1,$(strip $(value 2)))))
__check_defined = \
    $(if $(value $1),, \
      $(error Undefined $1$(if $2, ($2))))
#######

build: | $(BIN_DIR) $(DIST_DIR)
	@ $(BIN_DIR)/goreleaser build $(BUILD_FLAGS)

release: | $(BIN_DIR) $(DIST_DIR)
	@ $(BIN_DIR)/goreleaser release --clean

test: test/lint test/unit test/coverage

test/lint: | $(BIN_DIR)
	@ GOFLAGS="$(GOFLAGS)" \
	$(BIN_DIR)/golangci-lint run \
		--config $(GOLANGCILINT_CONFIG)

test/unit: $(COVERAGE_UNIT) | $(BIN_DIR)

test/coverage: $(COVER_DIR) $(COVERAGE_UNIT) $(COVERAGE_UNIT_INTERCHANGE) $(COVERATE_UNIT_HTML) $(COVERAGE_UNIT_XML) $(COVERAGE_COMBINED) $(COVERAGE_COMBINED_INTERCHANGE) $(COVERAGE_COMBINED_HTML) $(COVERAGE_COMBINED_XML) | $(BIN_DIR)
	@ $(GOCMD) tool cover -func $(COVERAGE_COMBINED)

BENCH_TRIGGER_SERIAL = $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableSerialChanges.txt
BENCH_TRIGGER_SERIAL_REPORT = $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableSerialChanges-cmp.csv
BENCH_TRIGGER_CONCURRENT = $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableConcurrentChanges.txt
BENCH_TRIGGER_CONCURRENT_REPORT = $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableConcurrentChanges-cmp.csv
BENCH_TRIGGER_LARGE_SERIAL = $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableSerialChanges.txt
BENCH_TRIGGER_LARGE_SERIAL_REPORT = $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableSerialChanges-cmp.csv
BENCH_TRIGGER_LARGE_CONCURRENT = $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableConcurrentChanges.txt
BENCH_TRIGGER_LARGE_CONCURRENT_REPORT = $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableConcurrentChanges-cmp.csv
BENCH_BLOB = $(BENCH_DIR)/BenchmarkBlobEncoding.txt
BENCH_BLOB_REPORT = $(BENCH_DIR)/BenchmarkBlobEncoding-cmp.csv

benchmarks: $(BENCH_TRIGGER_SERIAL) $(BENCH_TRIGGER_CONCURRENT) $(BENCH_TRIGGER_LARGE_SERIAL) $(BENCH_TRIGGER_LARGE_CONCURRENT) $(BENCH_BLOB)
benchmarks/reports: $(BENCH_TRIGGER_SERIAL_REPORT) $(BENCH_TRIGGER_CONCURRENT_REPORT) $(BENCH_TRIGGER_LARGE_SERIAL_REPORT) $(BENCH_TRIGGER_LARGE_CONCURRENT_REPORT) $(BENCH_BLOB_REPORT)

$(BENCH_TRIGGER_SERIAL): | $(BIN_DIR) $(BENCH_DIR)
	@ $(GOCMD) test -timeout 0 -run='^$$' -bench='^BenchmarkTriggerLatencySimpleTableSerialChanges*' -count=20 > $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableSerialChanges.txt
$(BENCH_TRIGGER_CONCURRENT): | $(BIN_DIR) $(BENCH_DIR)
	@ $(GOCMD) test -timeout 0 -run='^$$' -bench='^BenchmarkTriggerLatencySimpleTableConcurrentChanges*' -count=20 > $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableConcurrentChanges.txt
$(BENCH_TRIGGER_LARGE_SERIAL): | $(BIN_DIR) $(BENCH_DIR)
	@ $(GOCMD) test -timeout 0 -run='^$$' -bench='^BenchmarkTriggerLatencyLargeTableSerialChanges*' -count=20 > $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableSerialChanges.txt
$(BENCH_TRIGGER_LARGE_CONCURRENT): | $(BIN_DIR) $(BENCH_DIR)
	@ $(GOCMD) test -timeout 0 -run='^$$' -bench='^BenchmarkTriggerLatencyLargeTableConcurrentChanges*' -count=20 > $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableConcurrentChanges.txt
$(BENCH_BLOB): | $(BIN_DIR) $(BENCH_DIR)
	@ $(GOCMD) test -timeout 0 -run='^$$' -bench='^BenchmarkBlobEncoding*' -count=20 > $(BENCH_DIR)/BenchmarkBlobEncoding.txt

$(BENCH_JSON_REPORT): $(BENCH_JSON) | $(BIN_DIR)
	@ $(BIN_DIR)/benchstat -format csv -col /columns $(BENCH_DIR)/BenchmarkJSONCalculation.txt > $(BENCH_DIR)/BenchmarkJSONCalculation-cmp.csv
$(BENCH_TRIGGER_SERIAL_REPORT): $(BENCH_TRIGGER_SERIAL) | $(BIN_DIR)
	@ $(BIN_DIR)/benchstat -format csv -col /triggers -row /columns $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableSerialChanges.txt > $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableSerialChanges-cmp.csv
$(BENCH_TRIGGER_CONCURRENT_REPORT): $(BENCH_TRIGGER_CONCURRENT) | $(BIN_DIR)
	@ $(BIN_DIR)/benchstat -format csv -col /triggers -row /columns $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableConcurrentChanges.txt > $(BENCH_DIR)/BenchmarkTriggerLatencySimpleTableConcurrentChanges-cmp.csv
$(BENCH_TRIGGER_LARGE_SERIAL_REPORT): $(BENCH_TRIGGER_LARGE_SERIAL) | $(BIN_DIR)
	@ $(BIN_DIR)/benchstat -format csv -col /triggers -row /columns $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableSerialChanges.txt > $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableSerialChanges-cmp.csv
$(BENCH_TRIGGER_LARGE_CONCURRENT_REPORT): $(BENCH_TRIGGER_LARGE_CONCURRENT) | $(BIN_DIR)
	@ $(BIN_DIR)/benchstat -format csv -col /triggers -row /columns $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableConcurrentChanges.txt > $(BENCH_DIR)/BenchmarkTriggerLatencyLargeTableConcurrentChanges-cmp.csv
$(BENCH_BLOB_REPORT): $(BENCH_BLOB) | $(BIN_DIR)
	@ $(BIN_DIR)/benchstat -format csv -col /size $(BENCH_DIR)/BenchmarkBlobEncoding.txt > $(BENCH_DIR)/BenchmarkBlobEncoding-cmp.csv

tools: | $(BIN_DIR)
	@ cd $(TOOLS_DIR) && GOBIN=$(BIN_DIR) $(GOCMD) install $(GOTOOLS)
tools/update:
	@ cd $(TOOLS_DIR) && GOBIN=$(BIN_DIR) $(GOCMD) get -u
	@ cd $(TOOLS_DIR) && GOBIN=$(BIN_DIR) $(GOCMD) mod tidy

$(BIN_DIR):
	@ mkdir -p $(BIN_DIR)

generate:
	@ go generate ./...

fmt: | $(BIN_DIR)
	@ GOFLAGS="$(GOFLAGS)" \
	$(BIN_DIR)/goimports -w -v \
		-local $(GOIMPORT_LOCAL) \
		$(shell find . -type f -name '*.go' -not -path "./vendor/*")

clean: clean/test clean/tools clean/build
clean/build:
	@:$(call check_defined,BUILD_DIR)
	@ rm -rf "$(BUILD_DIR)"
	@:$(call check_defined,DIST_DIR)
	@ rm -rf "$(DIST_DIR)"
clean/test:
	@:$(call check_defined,COVER_DIR)
	@ rm -rf "$(COVER_DIR)"
clean/tools:
	@:$(call check_defined,BIN_DIR)
	@ rm -rf "$(BIN_DIR)"


$(COVERAGE_UNIT): $(shell find . -type f -name '*.go' -not -path "./vendor/*") | $(COVER_DIR)
	@ $(GOCMD) test \
		-v \
		-cover \
		-race \
		-coverprofile="$(COVERAGE_UNIT)" \
		./...

$(COVER_DIR)/%.interchange: $(COVER_DIR)/%.out
	@ GOFLAGS="$(GOFLAGS)" \
	$(BIN_DIR)/gocov convert $< > $@

$(COVER_DIR)/%.xml: $(COVER_DIR)/%.interchange
	@ cat $< | \
	GOFLAGS="$(GOFLAGS)" \
	$(BIN_DIR)/gocov-xml > $@

$(COVER_DIR)/%.html: $(COVER_DIR)/%.interchange
	@ cat $< | \
	GOFLAGS="$(GOFLAGS)" \
	$(BIN_DIR)/gocov-html > $@

$(COVERAGE_COMBINED):
	@ GOFLAGS="$(GOFLAGS)" \
 	$(BIN_DIR)/gocovmerge $(COVER_DIR)/*.out > $(COVERAGE_COMBINED)

$(COVER_DIR): | $(BUILD_DIR)
	@ mkdir -p $(COVER_DIR)

$(BENCH_DIR): | $(BUILD_DIR)
	@ mkdir -p $(BENCH_DIR)

$(BUILD_DIR):
	@ mkdir -p $(BUILD_DIR)
