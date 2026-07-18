# Install scripts into the falcos build tree for inclusion in the image.
# Default path assumes both repos are cloned side by side; override with
# FALCOS_REPO, e.g.:
#   make install-helpers FALCOS_REPO=/home/pc/Projects/falcos
FALCOS_REPO ?= ../falcos

# Destination paths mirror the image layout under build_files/files/common/.
HELPERS_DEST  = $(FALCOS_REPO)/build_files/files/common/usr/share/falcos/falcos-helpers.sh
PROGRESS_DEST = $(FALCOS_REPO)/build_files/files/common/usr/libexec/falcos-progress

.PHONY: install-helpers
install-helpers: $(HELPERS_DEST) $(PROGRESS_DEST)

$(HELPERS_DEST): scripts/falcos-helpers.sh
	cp $< $@
	chmod 644 $@
	@echo "Installed falcos-helpers.sh"

$(PROGRESS_DEST): scripts/falcos-progress
	cp $< $@
	chmod 755 $@
	@echo "Installed falcos-progress"
