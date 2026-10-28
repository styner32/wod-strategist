.PHONY: live-test live-test-prepare live-test-list test-live test-live-prepare test-live-list

live-test:
	@$(MAKE) --no-print-directory -C api test-live

live-test-prepare:
	@$(MAKE) --no-print-directory -C api test-live-prepare

live-test-list:
	@$(MAKE) --no-print-directory -C api test-live-list

test-live:
	@$(MAKE) --no-print-directory -C api test-live

test-live-prepare:
	@$(MAKE) --no-print-directory -C api test-live-prepare

test-live-list:
	@$(MAKE) --no-print-directory -C api test-live-list
