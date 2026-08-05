FROM python:3.11-alpine

WORKDIR /app

COPY cas_server.py .
COPY users.json .

ENV PYTHONUNBUFFERED=1
ENV CAS_PORT=8443
ENV USERS_FILE=/app/users.json

EXPOSE 8443

CMD ["python", "cas_server.py"]
