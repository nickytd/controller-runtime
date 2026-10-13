MODULES := $(shell find . -maxdepth 2 -name go.mod | xargs -I{} dirname {} | sort)
BUILD_MODULES := $(filter-out ./23-envtest-controller,$(MODULES))

.PHONY: build test vet tidy fix clean

build:
	@for m in $(BUILD_MODULES); do \
		bin=$$(basename $$m); \
		echo "==> $$m (-> $$m/$$bin)"; \
		(cd $$m && go build -o $$bin .) || exit 1; \
	done

clean:
	@for m in $(BUILD_MODULES); do \
		bin=$$(basename $$m); \
		rm -f $$m/$$bin; \
	done
	@echo "removed per-sample binaries"

# NOTE: cases 18/19/20 (webhooks) and 23 (controller) are envtest integration
# tests and need KUBEBUILDER_ASSETS set
# (e.g. export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)).
test:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go test ./...) || exit 1; \
	done

vet:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go vet ./...) || exit 1; \
	done

fix:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go fix ./...) || exit 1; \
	done

tidy:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go mod tidy) || exit 1; \
	done
