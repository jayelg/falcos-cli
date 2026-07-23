# Install scripts into the target image build tree.
# Default path assumes both repos are cloned side by side; override with
# IMAGE_REPO, e.g.:
#   make install-helpers IMAGE_REPO=/path/to/target-image-repo
IMAGE_REPO ?= ../target-image-repo

# Destination paths mirror the image layout under build_files/files/common/.
HELPERS_DEST  = $(IMAGE_REPO)/build_files/files/common/usr/share/goojust/goojust-helpers.sh
PROGRESS_DEST = $(IMAGE_REPO)/build_files/files/common/usr/libexec/goojust-progress

.PHONY: install-helpers
install-helpers: $(HELPERS_DEST) $(PROGRESS_DEST)

$(HELPERS_DEST): scripts/goojust-helpers.sh
	cp $< $@
	chmod 644 $@
	@echo "Installed goojust-helpers.sh"

$(PROGRESS_DEST): scripts/goojust-progress
	cp $< $@
	chmod 755 $@
	@echo "Installed goojust-progress"
