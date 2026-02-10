@echo off
set DO_AI_IDLE=2s
set DO_AI_SUBMIT_DELAY=3s
echo Starting test > C:\Users\Administrator\Desktop\status.log
C:\Users\Administrator\Desktop\do-ai.exe C:\Users\Administrator\Desktop\mock.exe > C:\Users\Administrator\Desktop\stdout.log 2> C:\Users\Administrator\Desktop\error.log
echo Finished test >> C:\Users\Administrator\Desktop\status.log
