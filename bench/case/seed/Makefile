test:
	@./tally.sh 1 2 3 | grep -qx 6
ci:
	@shellcheck tally.sh && ./tally.sh 1 2 3 | grep -qx 6
